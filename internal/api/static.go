package api

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// keyPlaceholder is replaced in index.html with the per-run access key.
const keyPlaceholder = "__MF_ACCESS_KEY__"

const missingUI = `<!doctype html><html lang="ko"><meta charset="utf-8"><title>MomentForge</title>
<body style="font-family:system-ui;margin:3rem;max-width:40rem;line-height:1.6">
<h1>화면 파일이 없습니다</h1>
<p>서버는 실행 중이지만 화면이 빌드되지 않았습니다. 저장소 폴더에서 <code>make build</code>를 실행한 뒤 <code>bin/momentforge</code>로 다시 시작하세요.
개발 중이라면 <code>make dev</code>로 실행하고 터미널에 표시된 개발 서버 주소로 접속하세요.</p>`

// static serves the built web app. Hashed files under /assets/ are cached;
// any other path without a file falls back to index.html (client routing).
func (h *handler) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" && name != "index.html" && h.cfg.Assets != nil {
		if info, err := fs.Stat(h.cfg.Assets, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, h.cfg.Assets, name)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
	}
	h.index(w)
}

func (h *handler) index(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var page []byte
	if h.cfg.Assets != nil {
		page, _ = fs.ReadFile(h.cfg.Assets, "index.html")
	}
	if page == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(missingUI))
		return
	}
	w.Write(bytes.ReplaceAll(page, []byte(keyPlaceholder), []byte(h.cfg.AccessKey)))
}
