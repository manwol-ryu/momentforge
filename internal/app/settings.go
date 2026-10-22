package app

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/asr"
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
	envASREngine        = "ASR_ENGINE"
	envASRPrefix        = "ASR_" // + engine + "_" + setting key, for Configurable engines' paths
	envSensitive        = "COLLECT_SENSITIVE"
	envIncludeComments  = "COLLECT_COMMENTS"
	envViewerAdaptive   = "LIVE_VIEWER_ADAPTIVE"
	envCutSearchRounds  = "AI_CUT_SEARCH_ROUNDS"    // cut analysis: analyze rounds after the first (0 = no searching)
	envCutSearchKB      = "AI_CUT_SEARCH_KB"        // cut analysis: text searches may add per candidate
	envCutConfirm       = "AI_CUT_CONFIRM_SEARCHES" // cut analysis: each search round's results wait for the user
	envBackupDir        = "BACKUP_DIR"              // where session backup bundles go ("" = <data>/backups)
	envBackupVideo      = "BACKUP_VIDEO"            // a new bundle includes the video by default
	envBackupHeight     = "BACKUP_VIDEO_HEIGHT"     // the tallest video rendition a bundle downloads
	// 일반 설정 (docs/REDESIGN.md 15, 2026-10-22): what runs by itself once candidates are found.
	envAutoAI            = "AUTO_AI_REVIEW"         // send to AI without a preview (the user agreed when turning it on)
	envAutoAIRequests    = "AUTO_AI_MAX_REQUESTS"   // requests one session's automatic AI work may send at most
	envAutoAICost        = "AUTO_AI_COST_CAP_USD"   // and what it may cost, for models with a known price
	envAutoFix           = "AUTO_AI_TRANSCRIPT_FIX" // with auto AI: fix a recognised transcript from its context first
	envAutoTranscribe    = "AUTO_TRANSCRIBE"        // a collected VOD is transcribed right after its analysis
	envLiveAudio         = "LIVE_RECORD_AUDIO"      // a LIVE capture keeps the broadcast's audio to transcribe its candidates
	envAITaskModelPrefix = "AI_TASK_MODEL_"         // + task + "_" + provider: the model one task uses
)

// Limits of the automatic AI work per session (일반 설정).
const (
	defaultAutoAIRequests = 60
	maxAutoAIRequests     = 500
	defaultAutoAICostUSD  = 3.0
)

// BackupHeights are the video qualities a bundle can download (docs/AI-CUT-ANALYSIS.md 2.5).
var BackupHeights = []int{1080, 720, 480, 360}

const defaultBackupHeight = 720

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
	// ASREngine is the speech recognition engine a transcription uses when
	// it is ready ("" for the first ready one); ASRPaths are Configurable
	// engines' settings (engine ID → setting key → path).
	ASREngine string
	ASRPaths  map[string]map[string]string
	// The cut analysis's search budget per candidate (docs/AI-CUT-ANALYSIS.md 3.2):
	// how many more analyze rounds searches may add, and how many KB of
	// evidence they may bring in.
	CutSearchRounds int
	CutSearchKB     int
	// CutConfirmSearches makes each search round's results wait for the
	// user (send or go on without them) before they are sent.
	CutConfirmSearches bool
	// Session backup bundles (docs/AI-CUT-ANALYSIS.md 2.5): the folder they
	// go in ("" for <data>/backups), whether a new bundle includes the
	// video, and the tallest rendition it downloads.
	BackupDir         string
	BackupVideo       bool
	BackupVideoHeight int
	// 일반 설정: AutoAI sends to AI without a preview once candidates are
	// found (explaining every candidate, after fixing a fresh transcript when
	// AutoTranscriptFix), within AutoAIMaxRequests requests and, for a model
	// with a known price, AutoAICostCapUSD per session. AutoTranscribe
	// transcribes a collected VOD right after its analysis; LiveAudio keeps a
	// LIVE capture's audio to transcribe its candidates.
	AutoAI            bool
	AutoAIMaxRequests int
	AutoAICostCapUSD  float64
	AutoTranscriptFix bool
	AutoTranscribe    bool
	LiveAudio         bool
	// AITaskModels: provider ID → task (ai.Tasks) → the model that task
	// uses; a missing one is the provider's recommendation for the task,
	// else the provider's model in AIModels, else its default.
	AITaskModels map[string]map[string]string
}

// SettingsInfo is Settings with the files that keep them and the keys.
type SettingsInfo struct {
	Settings
	File    string
	KeyFile string
	// BackupDefaultDir is where bundles go when BackupDir is empty.
	BackupDefaultDir string
	// AIProviderAuto: no provider is saved, so AIProvider is the one this
	// computer could use when the app started.
	AIProviderAuto bool
}

type settingsState struct {
	mu   sync.Mutex
	raw  []envEntry // settings.env as read or last written
	auto string     // the provider chosen when the app started, while none is saved
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
	number := func(name string, def, lo, hi int) int {
		if v, ok := lookupEnv(raw, name); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= lo && n <= hi {
				return n
			}
		}
		return def
	}
	dollars := func(name string, def float64) float64 {
		if v, ok := lookupEnv(raw, name); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && validCostCap(f) {
				return f
			}
		}
		return def
	}
	s := Settings{
		AIModels:           map[string]string{},
		AIEndpoints:        map[string]string{},
		ASRPaths:           map[string]map[string]string{},
		Sensitive:          flag(envSensitive, false),
		IncludeComments:    flag(envIncludeComments, true),
		ViewerAdaptive:     flag(envViewerAdaptive, true),
		CutSearchRounds:    number(envCutSearchRounds, cut.DefaultSearchRounds, 0, cut.MaxSearchRounds),
		CutSearchKB:        number(envCutSearchKB, cut.DefaultSearchKB, 0, cut.MaxSearchKB),
		CutConfirmSearches: flag(envCutConfirm, false),
		BackupVideo:        flag(envBackupVideo, true),
		BackupVideoHeight:  number(envBackupHeight, defaultBackupHeight, 1, 4320),
		AutoAI:             flag(envAutoAI, false),
		AutoAIMaxRequests:  number(envAutoAIRequests, defaultAutoAIRequests, 1, maxAutoAIRequests),
		AutoAICostCapUSD:   dollars(envAutoAICost, defaultAutoAICostUSD),
		AutoTranscriptFix:  flag(envAutoFix, true),
		AutoTranscribe:     flag(envAutoTranscribe, true),
		LiveAudio:          flag(envLiveAudio, true),
		AITaskModels:       map[string]map[string]string{},
	}
	if !slices.Contains(BackupHeights, s.BackupVideoHeight) {
		s.BackupVideoHeight = defaultBackupHeight
	}
	if d, _ := lookupEnv(raw, envBackupDir); validPath(d) && filepath.IsAbs(d) {
		s.BackupDir = d
	}
	if id, _ := lookupEnv(raw, envAIProvider); id != "" {
		if _, ok := a.cfg.AI.Get(id); ok {
			s.AIProvider = id
		}
	}
	auto := false
	if s.AIProvider == "" && a.settings.auto != "" {
		s.AIProvider, auto = a.settings.auto, true
	}
	for _, p := range a.cfg.AI.All() {
		if m, _ := lookupEnv(raw, envAIModelPrefix+envName(p.ID())); m != "" && validModel(m) {
			s.AIModels[p.ID()] = m
		}
		for _, task := range ai.Tasks {
			if m, _ := lookupEnv(raw, taskModelEnv(task, p.ID())); m != "" && validModel(m) {
				if s.AITaskModels[p.ID()] == nil {
					s.AITaskModels[p.ID()] = map[string]string{}
				}
				s.AITaskModels[p.ID()][task] = m
			}
		}
		if _, ok := p.(ai.Endpointed); ok {
			if u, _ := lookupEnv(raw, envAIEndpointPrefix+envName(p.ID())); u != "" {
				if n, err := ai.NormalizeEndpoint(u); err == nil && n != "" {
					s.AIEndpoints[p.ID()] = n
				}
			}
		}
	}
	if id, _ := lookupEnv(raw, envASREngine); id != "" {
		if _, ok := a.cfg.ASR.Get(id); ok {
			s.ASREngine = id
		}
	}
	for _, e := range a.cfg.ASR.All() {
		c, ok := e.(asr.Configurable)
		if !ok {
			continue
		}
		for _, st := range c.Settings() {
			if v, _ := lookupEnv(raw, asrEnvName(e.ID(), st.Key)); validPath(v) {
				if s.ASRPaths[e.ID()] == nil {
					s.ASRPaths[e.ID()] = map[string]string{}
				}
				s.ASRPaths[e.ID()][st.Key] = v
			}
		}
	}
	return SettingsInfo{Settings: s, File: a.settingsPath(), KeyFile: a.keyFilePath(), AIProviderAuto: auto,
		BackupDefaultDir: filepath.Join(a.cfg.DataDir, "backups")}
}

func asrEnvName(engine, key string) string {
	return envASRPrefix + envName(engine) + "_" + envName(key)
}

func taskModelEnv(task, provider string) string {
	return envAITaskModelPrefix + envName(task) + "_" + envName(provider)
}

// validCostCap is a positive limit up to the largest cost cap a review takes.
func validCostCap(f float64) bool { return f > 0 && f <= aiMaxCostCapUSD }

// TaskModel is the model a task uses with a provider: the one set for the
// task, else the provider's recommendation for it, else the provider's
// model in the settings, else its default.
func (a *App) TaskModel(p ai.Provider, task string) string {
	s := a.Settings()
	if m := s.AITaskModels[p.ID()][task]; m != "" {
		return m
	}
	if tm, ok := p.(ai.TaskModeler); ok {
		if m := tm.TaskModel(task); m != "" {
			return m
		}
	}
	if m := s.AIModels[p.ID()]; m != "" {
		return m
	}
	return p.DefaultModel()
}

// validPath is a non-empty path without control characters, up to 1024 bytes.
func validPath(p string) bool {
	return p != "" && len(p) <= 1024 && !strings.ContainsFunc(p, unicode.IsControl)
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
	for id, tasks := range s.AITaskModels {
		if _, ok := a.cfg.AI.Get(id); !ok {
			return SettingsInfo{}, domain.NewUserError("INPUT", "작업별 모델을 정할 AI 제공자를 목록에서 골라 주세요.")
		}
		for task := range tasks {
			if !slices.Contains(ai.Tasks, task) {
				return SettingsInfo{}, domain.NewUserError("INPUT", "모델을 정할 수 없는 작업입니다: "+task)
			}
		}
	}
	for _, p := range a.cfg.AI.All() { // registration and task order keep the file stable
		for _, task := range ai.Tasks {
			m := strings.TrimSpace(s.AITaskModels[p.ID()][task])
			if m == "" {
				continue
			}
			if !validModel(m) {
				return SettingsInfo{}, domain.NewUserError("INPUT",
					p.Name()+" 작업별 모델 ID를 확인해 주세요. 줄바꿈 없이 "+strconv.Itoa(aiMaxModelLength)+"자까지 쓸 수 있습니다.")
			}
			entries = append(entries, envEntry{taskModelEnv(task, p.ID()), m})
		}
	}
	if s.ASREngine != "" {
		if _, ok := a.cfg.ASR.Get(s.ASREngine); !ok {
			return SettingsInfo{}, domain.NewUserError("INPUT", "기본 음성 인식 엔진을 목록에서 골라 주세요.")
		}
		entries = append(entries, envEntry{envASREngine, s.ASREngine})
	}
	for id, values := range s.ASRPaths {
		e, ok := a.cfg.ASR.Get(id)
		c, can := e.(asr.Configurable)
		if !ok || !can {
			return SettingsInfo{}, domain.NewUserError("INPUT", "경로를 정할 수 없는 음성 인식 엔진입니다: "+id)
		}
		known := map[string]bool{}
		for _, st := range c.Settings() {
			known[st.Key] = true
		}
		for k, v := range values {
			if !known[k] {
				return SettingsInfo{}, domain.NewUserError("INPUT", e.Name()+"에 없는 설정 항목입니다: "+k)
			}
			if v = strings.TrimSpace(v); v != "" && !validPath(v) {
				return SettingsInfo{}, domain.NewUserError("INPUT", e.Name()+"의 "+k+" 경로를 확인해 주세요. 줄바꿈 없이 1024자까지 쓸 수 있습니다.")
			}
		}
	}
	for _, e := range a.cfg.ASR.All() { // registration order keeps the file stable
		c, ok := e.(asr.Configurable)
		if !ok {
			continue
		}
		for _, st := range c.Settings() {
			if v := strings.TrimSpace(s.ASRPaths[e.ID()][st.Key]); v != "" {
				entries = append(entries, envEntry{asrEnvName(e.ID(), st.Key), v})
			}
		}
	}
	if s.CutSearchRounds < 0 || s.CutSearchRounds > cut.MaxSearchRounds {
		return SettingsInfo{}, domain.NewUserError("INPUT", fmt.Sprintf("컷 분석의 검색 단계 수는 0~%d 사이여야 합니다.", cut.MaxSearchRounds))
	}
	if s.CutSearchKB < 0 || s.CutSearchKB > cut.MaxSearchKB {
		return SettingsInfo{}, domain.NewUserError("INPUT", fmt.Sprintf("컷 분석의 검색 추가 전송량은 0~%dKB 사이여야 합니다.", cut.MaxSearchKB))
	}
	if s.BackupDir = strings.TrimSpace(s.BackupDir); s.BackupDir != "" {
		if !validPath(s.BackupDir) || !filepath.IsAbs(s.BackupDir) {
			return SettingsInfo{}, domain.NewUserError("INPUT", "백업 묶음 폴더는 /로 시작하는 전체 경로로 적어 주세요. 비우면 데이터 폴더의 backups를 씁니다.")
		}
		entries = append(entries, envEntry{envBackupDir, filepath.Clean(s.BackupDir)})
	}
	if s.BackupVideoHeight == 0 {
		s.BackupVideoHeight = defaultBackupHeight
	}
	if !slices.Contains(BackupHeights, s.BackupVideoHeight) {
		return SettingsInfo{}, domain.NewUserError("INPUT", "백업 묶음의 영상 화질을 목록에서 골라 주세요.")
	}
	// Zero limits are the defaults (a caller that predates them).
	if s.AutoAIMaxRequests == 0 {
		s.AutoAIMaxRequests = defaultAutoAIRequests
	}
	if s.AutoAICostCapUSD == 0 {
		s.AutoAICostCapUSD = defaultAutoAICostUSD
	}
	if s.AutoAIMaxRequests < 1 || s.AutoAIMaxRequests > maxAutoAIRequests {
		return SettingsInfo{}, domain.NewUserError("INPUT", fmt.Sprintf("자동 AI 검토의 요청 한도는 1~%d회로 정해 주세요.", maxAutoAIRequests))
	}
	if !validCostCap(s.AutoAICostCapUSD) {
		return SettingsInfo{}, domain.NewUserError("INPUT", fmt.Sprintf("자동 AI 검토의 비용 한도는 0보다 크고 %d달러 이하로 정해 주세요.", aiMaxCostCapUSD))
	}
	entries = append(entries,
		envEntry{envSensitive, strconv.FormatBool(s.Sensitive)},
		envEntry{envIncludeComments, strconv.FormatBool(s.IncludeComments)},
		envEntry{envViewerAdaptive, strconv.FormatBool(s.ViewerAdaptive)},
		envEntry{envCutSearchRounds, strconv.Itoa(s.CutSearchRounds)},
		envEntry{envCutSearchKB, strconv.Itoa(s.CutSearchKB)},
		envEntry{envCutConfirm, strconv.FormatBool(s.CutConfirmSearches)},
		envEntry{envBackupVideo, strconv.FormatBool(s.BackupVideo)},
		envEntry{envBackupHeight, strconv.Itoa(s.BackupVideoHeight)},
		envEntry{envAutoAI, strconv.FormatBool(s.AutoAI)},
		envEntry{envAutoAIRequests, strconv.Itoa(s.AutoAIMaxRequests)},
		envEntry{envAutoAICost, strconv.FormatFloat(s.AutoAICostCapUSD, 'f', -1, 64)},
		envEntry{envAutoFix, strconv.FormatBool(s.AutoTranscriptFix)},
		envEntry{envAutoTranscribe, strconv.FormatBool(s.AutoTranscribe)},
		envEntry{envLiveAudio, strconv.FormatBool(s.LiveAudio)},
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
	a.applyASRSettings()
	return a.Settings(), nil
}

// applyASRSettings gives Configurable engines their saved paths (cleared
// where none is saved).
func (a *App) applyASRSettings() {
	saved := a.Settings().ASRPaths
	for _, e := range a.cfg.ASR.All() {
		if c, ok := e.(asr.Configurable); ok {
			values := saved[e.ID()]
			if values == nil {
				values = map[string]string{}
			}
			if err := c.Configure(values); err != nil {
				slog.Warn("saved speech engine paths not used", "engine", e.ID(), "err", err)
			}
		}
	}
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
