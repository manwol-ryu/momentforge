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
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

const (
	apiBase     = "https://api.chzzk.naver.com"
	apiHost     = "api.chzzk.naver.com"
	commentBase = "https://apis.naver.com/nng_main/nng_comment_api/v1"
	commentHost = "apis.naver.com"
	webHost     = "chzzk.naver.com"
	webOrigin   = "https://chzzk.naver.com"
	cdnOrigin   = "https://*.navercdn.com"
	cdnSuffix   = ".navercdn.com"
)

var (
	videoPath  = regexp.MustCompile(`^/video/([0-9]{1,20})/?$`)
	digitsOnly = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// Chzzk implements platform.Platform, VODSource, PlaybackSource, LiveSource,
// CommentSource and LinkSource.
type Chzzk struct {
	base     string
	http     *platform.HTTPClient
	comments commentEndpoint
	live     liveEndpoints
}

// Option configures the adapter (tests point it at a fake server).
type Option func(*Chzzk)

// WithAPIBase points the API, and the comment API, at a fake server so a
// test never reaches the real service by accident. Tests only.
func WithAPIBase(base string, host string) Option {
	return func(c *Chzzk) {
		c.base, c.http.AllowedHost = base, host
		c.comments.base, c.comments.http.AllowedHost = base, host
	}
}

// WithCommentBase replaces the comment API origin. Tests only.
func WithCommentBase(base string, host string) Option {
	return func(c *Chzzk) { c.comments.base, c.comments.http.AllowedHost = base, host }
}

// WithInterval changes the minimum gap between requests. Tests only.
func WithInterval(d time.Duration) Option {
	return func(c *Chzzk) { c.http.Interval, c.comments.http.Interval = d, d }
}

// WithSleep replaces waiting between retries. Tests only.
func WithSleep(f func(context.Context, time.Duration) error) Option {
	return func(c *Chzzk) { c.http.Sleep, c.comments.http.Sleep = f, f }
}

// New returns the adapter with CzzEdit's request etiquette.
func New(opts ...Option) *Chzzk {
	client := func(host string) *platform.HTTPClient {
		return &platform.HTTPClient{
			Header:      http.Header{"User-Agent": {"Mozilla/5.0"}, "Referer": {webOrigin + "/"}},
			Interval:    time.Second,
			Attempts:    5,
			MaxBytes:    8 << 20,
			MaxWait:     300 * time.Second,
			Timeout:     20 * time.Second,
			AllowedHost: host,
		}
	}
	c := &Chzzk{
		base:     apiBase,
		live:     defaultLive(),
		http:     client(apiHost),
		comments: commentEndpoint{base: commentBase, http: client(commentHost)},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (*Chzzk) ID() string             { return "chzzk" }
func (*Chzzk) Name() string           { return "치지직" }
func (*Chzzk) MediaOrigins() []string { return []string{cdnOrigin} }

// Match accepts https://chzzk.naver.com/video/<number>, a bare number (VOD),
// and https://chzzk.naver.com/live/<32-hex channel id> (LIVE).
func (*Chzzk) Match(raw string) (platform.Target, bool, error) {
	if digitsOnly.MatchString(raw) {
		return platform.Target{Platform: "chzzk", Kind: platform.KindVOD, ID: raw}, true, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() != webHost {
		return platform.Target{}, false, nil
	}
	safe := u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443")
	if m := videoPath.FindStringSubmatch(u.Path); m != nil && safe {
		return platform.Target{Platform: "chzzk", Kind: platform.KindVOD, ID: m[1]}, true, nil
	}
	if m := livePath.FindStringSubmatch(u.Path); m != nil && safe {
		return platform.Target{Platform: "chzzk", Kind: platform.KindLive, ID: strings.ToLower(m[1])}, true, nil
	}
	return platform.Target{}, false, domain.NewUserError("INVALID_URL",
		"다시보기는 https://chzzk.naver.com/video/영상번호 형식, 생방송은 https://chzzk.naver.com/live/채널ID 형식으로 입력해 주세요.")
}

// WatchURL opens the VOD on CHZZK at offsetMs, or the channel's live page.
func (*Chzzk) WatchURL(t platform.Target, offsetMs int64) string {
	if t.Kind == platform.KindLive {
		return webOrigin + "/live/" + t.ID
	}
	return webOrigin + "/video/" + t.ID + "?currentTime=" + strconv.FormatInt(max(0, offsetMs)/1000, 10)
}
