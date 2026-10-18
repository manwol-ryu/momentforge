package api

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/manwol-ryu/momentforge/internal/app"
)

// scanBackups lists the broadcasts in a backup folder (docs/AI-CUT-ANALYSIS.md 2.5a).
func (h *handler) scanBackups(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir string `json:"dir"`
	}
	if !readBody(w, r, &body) {
		return
	}
	list, err := h.cfg.Core.ScanBackups(r.Context(), body.Dir)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"broadcasts": list})
}

// importBackup imports one broadcast (its chat, then analysis, then the transcript when asked).
func (h *handler) importBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path       string `json:"path"`
		Transcribe bool   `json:"transcribe"`
		Sensitive  bool   `json:"sensitive"`
	}
	if !readBody(w, r, &body) {
		return
	}
	st, err := h.cfg.Core.StartImportBackup(app.ImportRequest{Path: body.Path, Transcribe: body.Transcribe, Sensitive: body.Sensitive})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

// asrEngines lists the speech recognition engines and whether they can run.
func (h *handler) asrEngines(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"engines": h.cfg.Core.ASREngines(r.Context())})
}

// transcribe runs speech recognition on a session's media file.
func (h *handler) transcribe(w http.ResponseWriter, r *http.Request) {
	st, err := h.cfg.Core.StartTranscribe(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

// transcript summarises the newest transcript (null when there is none).
func (h *handler) transcript(w http.ResponseWriter, r *http.Request) {
	v, ok, err := h.cfg.Core.Transcript(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// importCaption stores an uploaded SRT/WebVTT file as the session's
// transcript: multipart form with "file" and an optional "offsetMs".
func (h *handler) importCaption(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, app.CaptionMaxBytes+1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "INPUT", "자막 파일을 받지 못했습니다. 64MB 이하의 SRT·VTT 파일을 골라 주세요.")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "INPUT", "자막 파일을 골라 주세요.")
		return
	}
	defer f.Close()
	var offset int64
	if v := strings.TrimSpace(r.FormValue("offsetMs")); v != "" {
		if offset, err = strconv.ParseInt(v, 10, 64); err != nil {
			writeError(w, http.StatusBadRequest, "INPUT", "시간 오프셋은 밀리초 정수로 적어 주세요.")
			return
		}
	}
	v, err := h.cfg.Core.ImportCaption(r.Context(), chi.URLParam(r, "id"), header.Filename, f, offset)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type utteranceJSON struct {
	ID      string `json:"id"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

// utterances returns the transcript's lines in [from, to) (ms; to defaults to from + 10 minutes).
func (h *handler) utterances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 500
	}
	utts, err := h.cfg.Core.Utterances(r.Context(), chi.URLParam(r, "id"), max(0, from), to, limit)
	if err != nil {
		writeAppError(w, err)
		return
	}
	out := make([]utteranceJSON, len(utts))
	for i, u := range utts {
		out[i] = utteranceJSON{ID: u.ID, StartMs: u.StartMs, EndMs: u.EndMs, Speaker: u.Speaker, Text: u.Text}
	}
	writeJSON(w, http.StatusOK, map[string]any{"utterances": out})
}

var mediaTypes = map[string]string{".mp4": "video/mp4", ".m4a": "audio/mp4", ".mov": "video/quicktime", ".m4v": "video/mp4", ".mp3": "audio/mpeg", ".wav": "audio/wav"}

// media streams a session's own media file (an imported backup's video)
// to the app's player with range requests. The <video> element cannot send
// the access key header, so this route sits behind the Host and same-origin
// checks only; the session id is unguessable and the path comes from the
// database, never from the request.
func (h *handler) media(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if kind != "video" && kind != "audio" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "없는 미디어 종류입니다.")
		return
	}
	path, err := h.cfg.Core.MediaFile(r.Context(), chi.URLParam(r, "id"), kind)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if ct, ok := mediaTypes[strings.ToLower(filepath.Ext(path))]; ok {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeFile(w, r, path)
}
