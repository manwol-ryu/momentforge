package api

import (
	"net/http"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

type settingsJSON struct {
	AIProvider      string                       `json:"aiProvider"`
	AIModels        map[string]string            `json:"aiModels"`
	AIEndpoints     map[string]string            `json:"aiEndpoints"`
	Sensitive       bool                         `json:"sensitive"`
	IncludeComments bool                         `json:"includeComments"`
	ViewerAdaptive  bool                         `json:"viewerAdaptive"`
	ASREngine       string                       `json:"asrEngine"`
	ASRPaths        map[string]map[string]string `json:"asrPaths"`
	CutSearchRounds int                          `json:"cutSearchRounds"`
	CutSearchKB     int                          `json:"cutSearchKb"`
	BackupDir       string                       `json:"backupDir"`
	BackupVideo     bool                         `json:"backupVideo"`
	BackupHeight    int                          `json:"backupVideoHeight"`
	BackupHeights   []int                        `json:"backupVideoHeights"`
	BackupDefault   string                       `json:"backupDefaultDir"`
	File            string                       `json:"file"`
	KeyFile         string                       `json:"keyFile"`
	AIProviderAuto  bool                         `json:"aiProviderAuto"`
	// 일반 설정: what runs by itself once candidates are found, and its limits.
	AutoAI            bool                         `json:"autoAI"`
	AutoAIMaxRequests int                          `json:"autoAIMaxRequests"`
	AutoAICostCapUSD  float64                      `json:"autoAICostCapUsd"`
	AutoTranscriptFix bool                         `json:"autoTranscriptFix"`
	AutoTranscribe    bool                         `json:"autoTranscribe"`
	LiveAudio         bool                         `json:"liveAudio"`
	AITaskModels      map[string]map[string]string `json:"aiTaskModels"`
	CutConfirm        bool                         `json:"cutConfirmSearches"`
}

func toSettingsJSON(s app.SettingsInfo) settingsJSON {
	models, endpoints := s.AIModels, s.AIEndpoints
	if models == nil {
		models = map[string]string{}
	}
	if endpoints == nil {
		endpoints = map[string]string{}
	}
	paths := s.ASRPaths
	if paths == nil {
		paths = map[string]map[string]string{}
	}
	taskModels := s.AITaskModels
	if taskModels == nil {
		taskModels = map[string]map[string]string{}
	}
	return settingsJSON{AIProvider: s.AIProvider, AIModels: models, AIEndpoints: endpoints, Sensitive: s.Sensitive,
		IncludeComments: s.IncludeComments, ViewerAdaptive: s.ViewerAdaptive, ASREngine: s.ASREngine, ASRPaths: paths,
		CutSearchRounds: s.CutSearchRounds, CutSearchKB: s.CutSearchKB,
		BackupDir: s.BackupDir, BackupVideo: s.BackupVideo, BackupHeight: s.BackupVideoHeight, BackupHeights: app.BackupHeights,
		BackupDefault: s.BackupDefaultDir,
		File:          s.File, KeyFile: s.KeyFile, AIProviderAuto: s.AIProviderAuto,
		AutoAI: s.AutoAI, AutoAIMaxRequests: s.AutoAIMaxRequests, AutoAICostCapUSD: s.AutoAICostCapUSD,
		AutoTranscriptFix: s.AutoTranscriptFix, AutoTranscribe: s.AutoTranscribe, LiveAudio: s.LiveAudio,
		AITaskModels: taskModels, CutConfirm: s.CutConfirmSearches}
}

// settings returns the defaults the screens start from (nothing secret).
func (h *handler) settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toSettingsJSON(h.cfg.Core.Settings()))
}

// saveSettings replaces every default at once, so each one must be given.
func (h *handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AIProvider      *string                      `json:"aiProvider"`
		AIModels        map[string]string            `json:"aiModels"`
		AIEndpoints     map[string]string            `json:"aiEndpoints"`
		Sensitive       *bool                        `json:"sensitive"`
		IncludeComments *bool                        `json:"includeComments"`
		ViewerAdaptive  *bool                        `json:"viewerAdaptive"`
		ASREngine       string                       `json:"asrEngine"`
		ASRPaths        map[string]map[string]string `json:"asrPaths"`
		CutSearchRounds *int                         `json:"cutSearchRounds"`
		CutSearchKB     *int                         `json:"cutSearchKb"`
		// The bundle settings came later; a body without them keeps the saved ones.
		BackupDir    *string `json:"backupDir"`
		BackupVideo  *bool   `json:"backupVideo"`
		BackupHeight int     `json:"backupVideoHeight"`
		// So did 일반 설정 and the models per task.
		AutoAI            *bool                        `json:"autoAI"`
		AutoAIMaxRequests *int                         `json:"autoAIMaxRequests"`
		AutoAICostCapUSD  *float64                     `json:"autoAICostCapUsd"`
		AutoTranscriptFix *bool                        `json:"autoTranscriptFix"`
		AutoTranscribe    *bool                        `json:"autoTranscribe"`
		LiveAudio         *bool                        `json:"liveAudio"`
		AITaskModels      map[string]map[string]string `json:"aiTaskModels"`
		CutConfirm        *bool                        `json:"cutConfirmSearches"`
	}
	if !readBody(w, r, &body) {
		return
	}
	if body.AIProvider == nil || body.Sensitive == nil || body.IncludeComments == nil || body.ViewerAdaptive == nil ||
		body.CutSearchRounds == nil || body.CutSearchKB == nil {
		writeAppError(w, domain.NewUserError("INPUT", "설정 값이 빠졌습니다. 화면을 새로 고친 뒤 다시 저장하세요."))
		return
	}
	cur := h.cfg.Core.Settings()
	backupDir, backupVideo, backupHeight := cur.BackupDir, cur.BackupVideo, cur.BackupVideoHeight
	if body.BackupDir != nil {
		backupDir = *body.BackupDir
	}
	if body.BackupVideo != nil {
		backupVideo = *body.BackupVideo
	}
	if body.BackupHeight != 0 {
		backupHeight = body.BackupHeight
	}
	next := cur.Settings
	pick := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	pick(&next.AutoAI, body.AutoAI)
	pick(&next.AutoTranscriptFix, body.AutoTranscriptFix)
	pick(&next.AutoTranscribe, body.AutoTranscribe)
	pick(&next.LiveAudio, body.LiveAudio)
	pick(&next.CutConfirmSearches, body.CutConfirm)
	if body.AutoAIMaxRequests != nil {
		next.AutoAIMaxRequests = *body.AutoAIMaxRequests
	}
	if body.AutoAICostCapUSD != nil {
		next.AutoAICostCapUSD = *body.AutoAICostCapUSD
	}
	if body.AITaskModels != nil {
		next.AITaskModels = body.AITaskModels
	}
	s, err := h.cfg.Core.SaveSettings(app.Settings{AIProvider: *body.AIProvider, AIModels: body.AIModels, AIEndpoints: body.AIEndpoints,
		Sensitive: *body.Sensitive, IncludeComments: *body.IncludeComments, ViewerAdaptive: *body.ViewerAdaptive,
		ASREngine: body.ASREngine, ASRPaths: body.ASRPaths, CutSearchRounds: *body.CutSearchRounds, CutSearchKB: *body.CutSearchKB,
		BackupDir: backupDir, BackupVideo: backupVideo, BackupVideoHeight: backupHeight,
		AutoAI: next.AutoAI, AutoAIMaxRequests: next.AutoAIMaxRequests, AutoAICostCapUSD: next.AutoAICostCapUSD,
		AutoTranscriptFix: next.AutoTranscriptFix, AutoTranscribe: next.AutoTranscribe, LiveAudio: next.LiveAudio,
		AITaskModels: next.AITaskModels, CutConfirmSearches: next.CutConfirmSearches})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSettingsJSON(s))
}
