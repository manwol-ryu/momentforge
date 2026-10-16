// Package chzzk is the CHZZK (치지직) adapter: URL matching, VOD metadata and
// chat pages, and public playback. It uses CHZZK's web endpoints, which are
// unofficial and may change; every unexpected shape becomes ErrSchemaChanged.
package chzzk

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

const (
	apiBase   = "https://api.chzzk.naver.com"
	apiHost   = "api.chzzk.naver.com"
	webHost   = "chzzk.naver.com"
	webOrigin = "https://chzzk.naver.com"
	cdnOrigin = "https://*.navercdn.com"
	cdnSuffix = ".navercdn.com"
)

var (
	videoPath  = regexp.MustCompile(`^/video/([0-9]{1,20})/?$`)
	digitsOnly = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// Chzzk implements platform.Platform, VODSource, PlaybackSource and LinkSource.
type Chzzk struct {
	base string
	http *platform.HTTPClient
}

// Option configures the adapter (tests point it at a fake server).
type Option func(*Chzzk)

// WithAPIBase replaces the API origin. Tests only.
func WithAPIBase(base string, host string) Option {
	return func(c *Chzzk) { c.base, c.http.AllowedHost = base, host }
}

// WithInterval changes the minimum gap between requests. Tests only.
func WithInterval(d time.Duration) Option { return func(c *Chzzk) { c.http.Interval = d } }

// WithSleep replaces waiting between retries. Tests only.
func WithSleep(f func(context.Context, time.Duration) error) Option {
	return func(c *Chzzk) { c.http.Sleep = f }
}

// New returns the adapter with CzzEdit's request etiquette.
func New(opts ...Option) *Chzzk {
	c := &Chzzk{
		base: apiBase,
		http: &platform.HTTPClient{
			Header:      http.Header{"User-Agent": {"Mozilla/5.0"}, "Referer": {webOrigin + "/"}},
			Interval:    time.Second,
			Attempts:    5,
			MaxBytes:    8 << 20,
			MaxWait:     300 * time.Second,
			Timeout:     20 * time.Second,
			AllowedHost: apiHost,
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (*Chzzk) ID() string             { return "chzzk" }
func (*Chzzk) Name() string           { return "치지직" }
func (*Chzzk) MediaOrigins() []string { return []string{cdnOrigin} }

// Match accepts https://chzzk.naver.com/video/<number> or a bare number.
func (*Chzzk) Match(raw string) (platform.Target, bool, error) {
	if digitsOnly.MatchString(raw) {
		return platform.Target{Platform: "chzzk", Kind: platform.KindVOD, ID: raw}, true, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() != webHost {
		return platform.Target{}, false, nil
	}
	if m := videoPath.FindStringSubmatch(u.Path); m != nil && u.Scheme == "https" && u.User == nil &&
		(u.Port() == "" || u.Port() == "443") {
		return platform.Target{Platform: "chzzk", Kind: platform.KindVOD, ID: m[1]}, true, nil
	}
	return platform.Target{}, false, domain.NewUserError("INVALID_URL",
		"https://chzzk.naver.com/video/영상번호 형식으로 입력해 주세요.")
}

// WatchURL opens the VOD on CHZZK at offsetMs.
func (*Chzzk) WatchURL(t platform.Target, offsetMs int64) string {
	return webOrigin + "/video/" + t.ID + "?currentTime=" + strconv.FormatInt(max(0, offsetMs)/1000, 10)
}
