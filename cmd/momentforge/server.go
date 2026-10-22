package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/ai/anthropic"
	"github.com/manwol-ryu/momentforge/internal/ai/cliproxy"
	"github.com/manwol-ryu/momentforge/internal/ai/codex"
	"github.com/manwol-ryu/momentforge/internal/ai/openai"
	"github.com/manwol-ryu/momentforge/internal/ai/opencode"
	"github.com/manwol-ryu/momentforge/internal/ai/opencodex"
	"github.com/manwol-ryu/momentforge/internal/api"
	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/asr/applespeech"
	"github.com/manwol-ryu/momentforge/internal/asr/whisper"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/detect/rules"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/events"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/platform/chzzk"
	"github.com/manwol-ryu/momentforge/internal/store"
)

type options struct {
	Port    int // 0 picks a free port (tests)
	DataDir string
	Version string
	Assets  fs.FS
	// Platforms and AI override the registered adapters (tests use fakes).
	Platforms []platform.Platform
	AI        []ai.Provider
}

// server is a running instance and everything it must release on shutdown.
type server struct {
	URL     string
	Port    int
	DataDir string

	http    *http.Server
	core    *app.App
	lock    *store.ServerLock
	store   *store.Store
	keyFile string
	failed  chan error
}

// start assembles the app and begins serving on 127.0.0.1.
func start(ctx context.Context, opt options) (s *server, err error) {
	dataDir, err := filepath.Abs(opt.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data folder: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, domain.WrapUserError("DATA_DIR", "데이터 폴더를 만들 수 없습니다: "+dataDir+". 쓰기 권한이 있는 폴더를 --data로 지정하세요.", err)
	}

	var cleanup []func()
	defer func() {
		if err != nil {
			for i := len(cleanup) - 1; i >= 0; i-- {
				cleanup[i]()
			}
		}
	}()

	lock, err := store.AcquireServerLock(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	cleanup = append(cleanup, func() { lock.Release() })

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opt.Port)))
	if err != nil {
		return nil, domain.WrapUserError("PORT_IN_USE",
			fmt.Sprintf("포트 %d를 다른 프로그램이 쓰고 있습니다. --port %d처럼 다른 포트를 지정하세요.", opt.Port, opt.Port+1), err)
	}
	cleanup = append(cleanup, func() { ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	key, err := newAccessKey()
	if err != nil {
		return nil, err
	}
	keyFile := filepath.Join(dataDir, "runtime", "access-key")
	if err := writePrivateFile(keyFile, key); err != nil {
		return nil, err
	}
	cleanup = append(cleanup, func() { os.Remove(keyFile) })

	st, err := store.Open(ctx, dataDir, opt.Version)
	if err != nil {
		return nil, err
	}
	cleanup = append(cleanup, func() { st.Close() })

	bus := events.NewBus(256)
	core := app.New(app.Config{
		Version: opt.Version, DataDir: dataDir, Store: st, Bus: bus,
		Params: detect.DefaultParams(), Rules: rules.All(), Platforms: registerPlatforms(opt.Platforms),
		AI: registerAI(opt.AI), AIBatchPoll: extraBatchPoll, LiveAutoEvery: extraLiveAutoEvery,
		ASR: registerASR(dataDir, opt.Version),
	})
	if err := core.RecoverLive(ctx); err != nil {
		return nil, err
	}
	if err := core.RecoverAI(ctx); err != nil {
		return nil, err
	}
	httpSrv := &http.Server{
		Handler: api.NewHandler(api.Config{
			Core: core, Bus: bus, Port: port, AccessKey: key, Assets: opt.Assets, MediaOrigins: core.MediaOrigins(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	httpSrv.RegisterOnShutdown(bus.Close) // ends open SSE streams

	s = &server{
		URL:     "http://127.0.0.1:" + strconv.Itoa(port),
		Port:    port,
		DataDir: dataDir,
		http:    httpSrv,
		core:    core,
		lock:    lock,
		store:   st,
		keyFile: keyFile,
		failed:  make(chan error, 1),
	}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.failed <- err
		}
	}()
	return s, nil
}

// Failed reports a serve error that stopped the server.
func (s *server) Failed() <-chan error { return s.failed }

// Shutdown stops accepting requests, waits for active ones, and releases resources.
func (s *server) Shutdown(ctx context.Context) error {
	s.core.Shutdown(ctx) // pause a running collection at a safe point first
	err := s.http.Shutdown(ctx)
	err = errors.Join(err, s.store.Close())
	os.Remove(s.keyFile)
	return errors.Join(err, s.lock.Release())
}

// registerPlatforms is where adapters are registered (REDESIGN.md 4).
// extraPlatforms and extraAI are added by build-tagged files (e2efake.go for make e2e).
var (
	extraPlatforms     []platform.Platform
	extraAI            []ai.Provider
	extraASR           []asr.Engine
	extraBatchPoll     time.Duration // 0: the app's default
	extraLiveAutoEvery time.Duration // 0: the app's default
	// opencodeOptions, codexOptions, cliproxyOptions and opencodexOptions
	// configure the providers that use programs on this computer (the E2E
	// binary points them at missing ones so a test can never reach a real
	// service or read the user's CLIProxyAPI or OpenCodex config).
	opencodeOptions  []opencode.Option
	codexOptions     []codex.Option
	cliproxyOptions  []cliproxy.Option
	opencodexOptions []opencodex.Option
)

func registerAI(override []ai.Provider) *ai.Registry {
	r := &ai.Registry{}
	if override != nil {
		r.Register(override...)
		return r
	}
	// The order is the screens' order and the start order: with nothing saved, the
	// screens open with the first provider that can send (a key, or its own login).
	r.Register(anthropic.New(), codex.New(codexOptions...), opencode.New(opencodeOptions...),
		cliproxy.New(cliproxyOptions...), opencodex.New(opencodexOptions...), openai.New())
	r.Register(extraAI...)
	return r
}

// registerASR registers the speech recognition engines: macOS's own first
// (through the embedded helper), then whisper.cpp (a program the user
// installs and names in the settings), after whatever a build-tagged file adds.
func registerASR(dataDir, version string) *asr.Registry {
	r := &asr.Registry{}
	// Build-tagged engines first: the E2E binary's fake must win over the real one, which is ready on any Mac.
	r.Register(extraASR...)
	runtimeDir := filepath.Join(dataDir, "runtime")
	if len(speechHelper) > 0 {
		r.Register(applespeech.New(speechHelper, runtimeDir, version))
	}
	r.Register(whisper.New(runtimeDir))
	return r
}

func registerPlatforms(override []platform.Platform) *platform.Registry {
	r := &platform.Registry{}
	if override != nil {
		r.Register(override...)
		return r
	}
	r.Register(chzzk.New())
	r.Register(extraPlatforms...)
	return r
}

func newAccessKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate access key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// writePrivateFile writes data readable only by the current user.
func writePrivateFile(path, data string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return os.Chmod(path, 0o600)
}
