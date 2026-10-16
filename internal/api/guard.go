package api

import (
	"crypto/subtle"
	"net/http"
	"strconv"
)

// guard enforces the local-only rules: the request must name this server by
// a loopback host, come from this server's own page, and carry the run key.
type guard struct {
	hosts   map[string]bool
	origins map[string]bool
	key     []byte
}

func newGuard(port int, key string) guard {
	p := strconv.Itoa(port)
	return guard{
		hosts:   map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true},
		origins: map[string]bool{"http://127.0.0.1:" + p: true, "http://localhost:" + p: true},
		key:     []byte(key),
	}
}

// localHost blocks DNS-rebinding: the Host header must be a loopback name.
func (g guard) localHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.hosts[r.Host] {
			writeError(w, http.StatusForbidden, "BAD_HOST", "로컬 주소(127.0.0.1)로 접속해 주세요.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects API calls made from other sites, including other local ports.
func (g guard) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && !g.origins[o] {
			writeError(w, http.StatusForbidden, "BAD_ORIGIN", "다른 사이트에서 보낸 요청은 처리하지 않습니다.")
			return
		}
		switch r.Header.Get("Sec-Fetch-Site") {
		case "cross-site", "same-site":
			writeError(w, http.StatusForbidden, "BAD_ORIGIN", "다른 사이트에서 보낸 요청은 처리하지 않습니다.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// accessKey requires the per-run key that only this server's page knows.
func (g guard) accessKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get(KeyHeader))
		if len(g.key) == 0 || subtle.ConstantTimeCompare(got, g.key) != 1 {
			writeError(w, http.StatusUnauthorized, "BAD_KEY", "접근 키가 맞지 않습니다. 앱을 다시 실행했다면 페이지를 새로고침하세요.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders applies to every response. Video may stream from the
// platforms' media origins; everything else must come from this server.
func securityHeaders(mediaOrigins []string) func(http.Handler) http.Handler {
	csp := cspFor(mediaOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Content-Security-Policy", csp)
			h.Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	}
}
