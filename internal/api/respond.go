package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write json response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// writeAppError shows UserErrors as is and hides other errors behind a generic message.
func writeAppError(w http.ResponseWriter, err error) {
	if ue, ok := domain.AsUserError(err); ok {
		writeError(w, http.StatusBadRequest, ue.Code, ue.Message)
		return
	}
	slog.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "INTERNAL", "처리 중 오류가 났습니다. 터미널에 표시된 오류를 확인하세요.")
}
