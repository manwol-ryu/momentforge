package ai

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// MaxEndpointLength bounds an address the user types.
const MaxEndpointLength = 300

// NormalizeEndpoint checks an address for an Endpointed provider: an http
// or https URL with a host and nothing after the path, without a trailing
// slash. "" means the provider's default.
func NormalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	bad := domain.NewUserError("INPUT",
		"주소는 http:// 또는 https://로 시작하는 API 주소로 넣어 주세요 (예: http://127.0.0.1:8317/v1).")
	if len(raw) > MaxEndpointLength || strings.ContainsFunc(raw, unicode.IsSpace) || strings.ContainsFunc(raw, unicode.IsControl) {
		return "", bad
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", bad
	}
	return strings.TrimRight(u.String(), "/"), nil
}
