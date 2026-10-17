package app

import (
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Files in the data folder that the settings screen writes
// (docs/REDESIGN.md 15). Both are only ever written by this app, 0600.
const (
	settingsFileName = "settings.env" // the defaults below; nothing secret
	keyFileName      = ".env"         // API keys the user chose to keep on this computer
)

// Names in settings.env. A model line per provider is "AI_MODEL_" + its ID,
// and an address line per Endpointed provider "AI_ENDPOINT_" + its ID.
const (
	envAIProvider       = "AI_PROVIDER"
	envAIModelPrefix    = "AI_MODEL_"
	envAIEndpointPrefix = "AI_ENDPOINT_"
	envSensitive        = "COLLECT_SENSITIVE"
	envIncludeComments  = "COLLECT_COMMENTS"
	envViewerAdaptive   = "LIVE_VIEWER_ADAPTIVE"
)

var settingsHeader = []string{
	"MomentForge 기본값. 설정 화면에서 저장하면 이 파일을 다시 씁니다.",
	"API 키는 이 파일에 두지 않습니다.",
}

var keyFileHeader = []string{
	"MomentForge API 키. 설정 화면에서 \"이 컴퓨터에 저장\"을 고른 키만 평문으로 들어 있습니다.",
	"공유하거나 커밋하지 마세요. 키를 지우면 이 줄도 지웁니다.",
}

// Settings are the defaults the screens start from: the AI provider and
// model the AI review opens with, and how the collect forms start. A form
// can still change them for one job.
type Settings struct {
	AIProvider      string            // "" for the first registered provider
	AIModels        map[string]string // provider ID → model; a missing one uses the provider's own
	AIEndpoints     map[string]string // Endpointed provider ID → address; a missing one uses the provider's default
	Sensitive       bool              // "항상 가장 민감하게"
	IncludeComments bool              // a VOD collection also reads the video's comments
	ViewerAdaptive  bool              // a LIVE capture adapts to viewer-count changes
}

// SettingsInfo is Settings with the files that keep them and the keys.
type SettingsInfo struct {
	Settings
	File    string
	KeyFile string
}

type settingsState struct {
	mu  sync.Mutex
	raw []envEntry // settings.env as read or last written
}

func (a *App) settingsPath() string { return filepath.Join(a.cfg.DataDir, settingsFileName) }
func (a *App) keyFilePath() string  { return filepath.Join(a.cfg.DataDir, keyFileName) }

func (a *App) initSettings() {
	if a.cfg.DataDir == "" {
		return
	}
	raw, err := readEnvFile(a.settingsPath())
	if err != nil {
		slog.Warn("settings file unreadable; using defaults", "err", err)
	}
	a.settings.raw = raw
}

// Settings returns the saved defaults. Entries for providers this build
// does not have are left out.
func (a *App) Settings() SettingsInfo {
	a.settings.mu.Lock()
	raw := a.settings.raw
	a.settings.mu.Unlock()
	flag := func(name string, def bool) bool {
		if v, ok := lookupEnv(raw, name); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				return b
			}
		}
		return def
	}
	s := Settings{
		AIModels:        map[string]string{},
		AIEndpoints:     map[string]string{},
		Sensitive:       flag(envSensitive, false),
		IncludeComments: flag(envIncludeComments, true),
		ViewerAdaptive:  flag(envViewerAdaptive, true),
	}
	if id, _ := lookupEnv(raw, envAIProvider); id != "" {
		if _, ok := a.cfg.AI.Get(id); ok {
			s.AIProvider = id
		}
	}
	for _, p := range a.cfg.AI.All() {
		if m, _ := lookupEnv(raw, envAIModelPrefix+envName(p.ID())); m != "" && validModel(m) {
			s.AIModels[p.ID()] = m
		}
		if _, ok := p.(ai.Endpointed); ok {
			if u, _ := lookupEnv(raw, envAIEndpointPrefix+envName(p.ID())); u != "" {
				if n, err := ai.NormalizeEndpoint(u); err == nil && n != "" {
					s.AIEndpoints[p.ID()] = n
				}
			}
		}
	}
	return SettingsInfo{Settings: s, File: a.settingsPath(), KeyFile: a.keyFilePath()}
}

// SaveSettings checks and writes the defaults to settings.env.
func (a *App) SaveSettings(s Settings) (SettingsInfo, error) {
	if a.cfg.DataDir == "" {
		return SettingsInfo{}, domain.NewUserError("SETTINGS_UNAVAILABLE", "데이터 폴더가 없어 설정을 저장할 수 없습니다.")
	}
	var entries []envEntry
	if s.AIProvider != "" {
		if _, ok := a.cfg.AI.Get(s.AIProvider); !ok {
			return SettingsInfo{}, domain.NewUserError("INPUT", "기본 AI 제공자를 목록에서 골라 주세요.")
		}
		entries = append(entries, envEntry{envAIProvider, s.AIProvider})
	}
	for id := range s.AIModels {
		if _, ok := a.cfg.AI.Get(id); !ok {
			return SettingsInfo{}, domain.NewUserError("INPUT", "기본 모델을 정할 AI 제공자를 목록에서 골라 주세요.")
		}
	}
	endpoints := map[string]string{} // checked addresses, applied once the file is written
	for id, u := range s.AIEndpoints {
		p, ok := a.cfg.AI.Get(id)
		if _, endpointed := p.(ai.Endpointed); !ok || !endpointed {
			return SettingsInfo{}, domain.NewUserError("INPUT", "주소를 정할 수 없는 AI 제공자입니다.")
		}
		n, err := ai.NormalizeEndpoint(u)
		if err != nil {
			return SettingsInfo{}, err
		}
		endpoints[id] = n
	}
	for _, p := range a.cfg.AI.All() { // registration order keeps the file stable
		m := strings.TrimSpace(s.AIModels[p.ID()])
		if m == "" {
			continue
		}
		if !validModel(m) {
			return SettingsInfo{}, domain.NewUserError("INPUT",
				p.Name()+" 기본 모델 ID를 확인해 주세요. 줄바꿈 없이 "+strconv.Itoa(aiMaxModelLength)+"자까지 쓸 수 있습니다.")
		}
		entries = append(entries, envEntry{envAIModelPrefix + envName(p.ID()), m})
	}
	for _, p := range a.cfg.AI.All() {
		if u := endpoints[p.ID()]; u != "" {
			entries = append(entries, envEntry{envAIEndpointPrefix + envName(p.ID()), u})
		}
	}
	entries = append(entries,
		envEntry{envSensitive, strconv.FormatBool(s.Sensitive)},
		envEntry{envIncludeComments, strconv.FormatBool(s.IncludeComments)},
		envEntry{envViewerAdaptive, strconv.FormatBool(s.ViewerAdaptive)},
	)
	a.settings.mu.Lock()
	err := writeEnvFile(a.settingsPath(), settingsHeader, entries)
	if err == nil {
		a.settings.raw = entries
	}
	a.settings.mu.Unlock()
	if err != nil {
		return SettingsInfo{}, domain.WrapUserError("SETTINGS_WRITE",
			"설정 파일을 쓸 수 없습니다: "+a.settingsPath()+". 데이터 폴더의 쓰기 권한을 확인하세요.", err)
	}
	a.applyEndpoints()
	return a.Settings(), nil
}

// applyEndpoints points Endpointed providers at the saved addresses (the
// default where none is saved).
func (a *App) applyEndpoints() {
	saved := a.Settings().AIEndpoints
	for _, p := range a.cfg.AI.All() {
		if e, ok := p.(ai.Endpointed); ok {
			if err := e.SetEndpoint(saved[p.ID()]); err != nil {
				slog.Warn("saved AI address not used", "provider", p.ID(), "err", err)
			}
		}
	}
}

func validModel(m string) bool {
	return m != "" && len(m) <= aiMaxModelLength && !strings.ContainsFunc(m, unicode.IsControl)
}
