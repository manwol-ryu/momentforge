package app

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/events"
)

// reopen starts another App on a's data folder, as the next run would.
func reopen(t *testing.T, a *App, providers ...ai.Provider) *App {
	t.Helper()
	reg := &ai.Registry{}
	reg.Register(providers...)
	b := New(Config{Version: "test", DataDir: a.cfg.DataDir, Store: a.cfg.Store, Bus: events.NewBus(16),
		Params: detect.DefaultParams(), AI: reg})
	t.Cleanup(func() { b.Shutdown(context.Background()) })
	return b
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("%s permissions = %v, want 0600", filepath.Base(path), info.Mode().Perm())
	}
}

func TestSettingsDefaultsSaveAndReload(t *testing.T) {
	f := &fakeAI{}
	a := newAIApp(t, f)
	s := a.Settings()
	if s.AIProvider != "" || len(s.AIModels) != 0 || s.Sensitive || !s.IncludeComments || !s.ViewerAdaptive {
		t.Fatalf("defaults = %+v", s.Settings)
	}
	if s.File != filepath.Join(a.cfg.DataDir, "settings.env") || s.KeyFile != filepath.Join(a.cfg.DataDir, ".env") {
		t.Fatalf("files = %s, %s", s.File, s.KeyFile)
	}
	if _, err := os.Stat(s.File); !os.IsNotExist(err) {
		t.Fatal("reading the defaults must not create the file")
	}

	saved, err := a.SaveSettings(Settings{AIProvider: "fakeai", AIModels: map[string]string{"fakeai": " model-x "},
		Sensitive: true, IncludeComments: false, ViewerAdaptive: false})
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{AIProvider: "fakeai", AIModels: map[string]string{"fakeai": "model-x"}, Sensitive: true}
	if saved.AIProvider != want.AIProvider || saved.AIModels["fakeai"] != "model-x" || !saved.Sensitive ||
		saved.IncludeComments || saved.ViewerAdaptive {
		t.Fatalf("saved = %+v", saved.Settings)
	}
	body := readFile(t, s.File)
	for _, line := range []string{"AI_PROVIDER=fakeai", "AI_MODEL_FAKEAI=model-x", "COLLECT_SENSITIVE=true",
		"COLLECT_COMMENTS=false", "LIVE_VIEWER_ADAPTIVE=false"} {
		if !strings.Contains(body, line+"\n") {
			t.Fatalf("settings.env lacks %q:\n%s", line, body)
		}
	}
	assertPrivate(t, s.File)

	// The next run starts from the file.
	b := reopen(t, a, f)
	if got := b.Settings(); got.AIProvider != "fakeai" || got.AIModels["fakeai"] != "model-x" || !got.Sensitive ||
		got.IncludeComments || got.ViewerAdaptive {
		t.Fatalf("after restart = %+v", got.Settings)
	}
	// A build without the provider ignores its lines.
	c := reopen(t, a, &pricedAI{})
	if got := c.Settings(); got.AIProvider != "" || len(got.AIModels) != 0 || !got.Sensitive {
		t.Fatalf("other build = %+v", got.Settings)
	}
}

func TestSettingsReadHandEditedFile(t *testing.T) {
	a := newAIApp(t, &fakeAI{})
	edited := "# 손으로 고침\n\nexport AI_PROVIDER = \"fakeai\"\nAI_MODEL_FAKEAI='m 2'\nCOLLECT_COMMENTS=no-such-bool\n" +
		"not a line\nCOLLECT_SENSITIVE=1\n"
	if err := os.WriteFile(filepath.Join(a.cfg.DataDir, "settings.env"), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	b := reopen(t, a, &fakeAI{})
	s := b.Settings()
	if s.AIProvider != "fakeai" || s.AIModels["fakeai"] != "m 2" || !s.Sensitive || !s.IncludeComments {
		t.Fatalf("hand-edited = %+v", s.Settings)
	}
}

func TestSettingsRejectBadValues(t *testing.T) {
	a := newAIApp(t, &fakeAI{})
	cases := map[string]Settings{
		"unknown provider":      {AIProvider: "nope"},
		"unknown model owner":   {AIModels: map[string]string{"nope": "m"}},
		"model with a new line": {AIModels: map[string]string{"fakeai": "m\nAI_PROVIDER=x"}},
		"model too long":        {AIModels: map[string]string{"fakeai": strings.Repeat("m", aiMaxModelLength+1)}},
	}
	for name, s := range cases {
		if _, err := a.SaveSettings(s); !isCode(err, "INPUT") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(a.cfg.DataDir, "settings.env")); !os.IsNotExist(err) {
		t.Fatal("a rejected save must not write the file")
	}
}

func TestAIKeyKeptOnThisComputer(t *testing.T) {
	f := &fakeAI{}
	a := newAIApp(t, f)
	keyFile := filepath.Join(a.cfg.DataDir, ".env")
	// A line the user added by hand stays.
	if err := os.WriteFile(keyFile, []byte("OTHER_TOOL_KEY=keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	src, err := a.SetAIKey("fakeai", " secret-1 ", true)
	if err != nil || src != KeyFromFile {
		t.Fatalf("persist: %q %v", src, err)
	}
	body := readFile(t, keyFile)
	if !strings.Contains(body, "MOMENTFORGE_FAKEAI_API_KEY=secret-1\n") || !strings.Contains(body, "OTHER_TOOL_KEY=keep-me\n") {
		t.Fatalf(".env:\n%s", body)
	}
	assertPrivate(t, keyFile)

	// The next run has the key without entering it again.
	b := reopen(t, a, f)
	if p := b.AIProviders()[0]; !p.Configured || p.KeySource != KeyFromFile || p.KeyName != "MOMENTFORGE_FAKEAI_API_KEY" {
		t.Fatalf("after restart = %+v", p)
	}
	if k, err := b.providerKey(f); err != nil || k != "secret-1" {
		t.Fatalf("key after restart = %q %v", k, err)
	}

	// Saving for this run only takes the key out of the file.
	if src, _ := b.SetAIKey("fakeai", "secret-2", false); src != KeyInMemory {
		t.Fatalf("memory only: %q", src)
	}
	if body := readFile(t, keyFile); strings.Contains(body, "secret") || !strings.Contains(body, "keep-me") {
		t.Fatalf(".env after a memory-only key:\n%s", body)
	}
	if p := reopen(t, a, f).AIProviders()[0]; p.Configured {
		t.Fatal("a memory-only key must not come back after a restart")
	}

	// Clearing removes it from memory and from the file.
	b.SetAIKey("fakeai", "secret-3", true)
	if src, err := b.SetAIKey("fakeai", "", true); src != "" || err != nil {
		t.Fatalf("clear: %q %v", src, err)
	}
	if p := b.AIProviders()[0]; p.Configured || p.KeySource != "" {
		t.Fatalf("after clear = %+v", p)
	}
	if body := readFile(t, keyFile); strings.Contains(body, "secret") {
		t.Fatalf(".env after clear:\n%s", body)
	}

	// The last key gone, the file goes too.
	os.WriteFile(keyFile, nil, 0o600)
	b.SetAIKey("fakeai", "secret-4", true)
	b.SetAIKey("fakeai", "", false)
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatal("an empty key file should be removed")
	}

	// No key was written anywhere else in the data folder (the database included).
	b.SetAIKey("fakeai", "secret-5", true)
	filepath.WalkDir(a.cfg.DataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == keyFile {
			return err
		}
		if strings.Contains(readFile(t, path), "secret-") {
			t.Errorf("a key reached %s", path)
		}
		return nil
	})
}

// envAI reads its key from an environment variable, like Claude and OpenAI.
type envAI struct{ fakeAI }

func (*envAI) KeyEnv() string { return "MF_TEST_API_KEY" }

func TestAIKeyFileWinsOverEnvironment(t *testing.T) {
	t.Setenv("MF_TEST_API_KEY", "from-env")
	e := &envAI{}
	a := newAIApp(t, &fakeAI{})
	a = reopen(t, a, e)
	if p := a.AIProviders()[0]; p.KeySource != KeyFromEnv || p.KeyName != "MF_TEST_API_KEY" {
		t.Fatalf("env key = %+v", p)
	}
	a.SetAIKey("fakeai", "from-file", true)
	if body := readFile(t, a.keyFilePath()); !strings.Contains(body, "MF_TEST_API_KEY=from-file\n") {
		t.Fatalf("the key file uses the provider's variable name:\n%s", body)
	}
	b := reopen(t, a, e)
	if k, _ := b.providerKey(e); k != "from-file" || b.AIProviders()[0].KeySource != KeyFromFile {
		t.Fatalf("file key = %q %+v", k, b.AIProviders()[0])
	}
	// Cleared here, the environment's key returns only with the next run.
	b.SetAIKey("fakeai", "", false)
	if b.AIProviders()[0].Configured {
		t.Fatal("cleared key still in memory")
	}
	if p := reopen(t, a, e).AIProviders()[0]; p.KeySource != KeyFromEnv {
		t.Fatalf("next run = %+v", p)
	}
}

// proxyAI sends to an address the user sets, like CLIProxyAPI.
type proxyAI struct {
	*fakeAI
	endpoint string
}

func (p *proxyAI) ID() string              { return "proxy" }
func (p *proxyAI) DefaultEndpoint() string { return "http://127.0.0.1:8317/v1" }
func (p *proxyAI) Endpoint() string {
	if p.endpoint == "" {
		return p.DefaultEndpoint()
	}
	return p.endpoint
}
func (p *proxyAI) SetEndpoint(u string) error {
	n, err := ai.NormalizeEndpoint(u)
	if err == nil {
		p.endpoint = n
	}
	return err
}

func TestSettingsKeepAProxyAddress(t *testing.T) {
	p := &proxyAI{fakeAI: &fakeAI{}}
	a := newAIApp(t, &fakeAI{})
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(p)
	a.initAI()
	if got := a.AIProviders()[0]; got.Endpoint != p.DefaultEndpoint() || got.DefaultEndpoint != p.DefaultEndpoint() {
		t.Fatalf("provider %+v", got)
	}
	if _, err := a.SaveSettings(Settings{AIEndpoints: map[string]string{"proxy": "ftp://x"}}); !isCode(err, "INPUT") {
		t.Fatalf("bad address: %v", err)
	}
	saved, err := a.SaveSettings(Settings{AIEndpoints: map[string]string{"proxy": " http://127.0.0.1:9000/v1/ "}})
	if err != nil || saved.AIEndpoints["proxy"] != "http://127.0.0.1:9000/v1" {
		t.Fatalf("saved %+v err %v", saved.Settings, err)
	}
	if p.Endpoint() != "http://127.0.0.1:9000/v1" || a.AIProviders()[0].Endpoint != "http://127.0.0.1:9000/v1" {
		t.Fatalf("not applied: %s", p.Endpoint())
	}
	if !strings.Contains(readFile(t, saved.File), "AI_ENDPOINT_PROXY=http://127.0.0.1:9000/v1\n") {
		t.Fatal("settings.env lacks the address")
	}
	// The next run points the provider at the saved address; clearing it goes back to the default.
	q := &proxyAI{fakeAI: &fakeAI{}}
	b := reopen(t, a, q)
	if q.Endpoint() != "http://127.0.0.1:9000/v1" {
		t.Fatalf("after restart: %s", q.Endpoint())
	}
	if _, err := b.SaveSettings(Settings{}); err != nil || q.Endpoint() != q.DefaultEndpoint() {
		t.Fatalf("cleared: %v %s", err, q.Endpoint())
	}
	if _, err := a.SaveSettings(Settings{AIEndpoints: map[string]string{"nope": "http://h/v1"}}); !isCode(err, "INPUT") {
		t.Fatalf("unknown provider: %v", err)
	}
}
