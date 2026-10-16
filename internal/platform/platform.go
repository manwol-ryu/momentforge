// Package platform defines what a streaming platform adapter provides and
// the registry that picks an adapter for a URL. Adapters live in
// internal/platform/<name> and are registered in cmd/momentforge.
package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// ErrSchemaChanged wraps every "the platform answered in an unexpected
// shape" failure. Adapters return it instead of guessing.
var ErrSchemaChanged = errors.New("platform response format changed")

// Kind is what a URL points at.
type Kind string

const (
	KindVOD  Kind = "vod"
	KindLive Kind = "live"
)

// Target is a resolved URL.
type Target struct {
	Platform string // adapter ID, e.g. "chzzk"
	Kind     Kind
	ID       string // platform's video or channel id
}

// Platform is the minimum every adapter implements. Optional abilities are
// separate interfaces (VODSource, PlaybackSource, …) checked at runtime.
type Platform interface {
	ID() string
	Name() string // shown to users, e.g. "치지직"
	// Match reports whether raw belongs to this platform. ok is false for
	// other platforms; err explains a URL that is this platform's but malformed.
	Match(raw string) (t Target, ok bool, err error)
	// MediaOrigins are https origins the browser may load video from
	// (added to the page's Content-Security-Policy).
	MediaOrigins() []string
}

// VODMeta describes a finished broadcast.
type VODMeta struct {
	Title      string
	DurationMs int64
}

// RawChat is one message as the platform reports it. AuthorID is the
// platform's original identifier; it must not be stored (app pseudonymizes it).
type RawChat struct {
	OffsetMs int64
	AuthorID string // empty when the platform gave none
	Text     string
}

// ChatPage is one page of VOD chat starting at a cursor.
type ChatPage struct {
	Messages []RawChat
	Next     int64 // cursor for the next page; meaningful only when !Finished
	Finished bool
	Raw      []byte       // original response body, kept for resume and diagnosis
	Gaps     []domain.Gap // spans the page could not certify (e.g. ambiguous boundaries)
}

// PageError is a page the adapter rejected; Raw is kept as quarantined evidence.
type PageError struct {
	Raw []byte
	Err error
}

func (e *PageError) Error() string { return e.Err.Error() }
func (e *PageError) Unwrap() error { return e.Err }

// VODSource collects chat from a finished broadcast.
type VODSource interface {
	VODMeta(ctx context.Context, t Target) (VODMeta, error)
	VODChats(ctx context.Context, t Target, cursor int64, durationMs int64) (ChatPage, error)
}

// Playback is a public stream the browser can play.
type Playback struct {
	URL  string
	Type string // "hls"
}

// PlaybackSource resolves a public stream for a VOD. URLs may be signed
// and short-lived; callers must not store them.
type PlaybackSource interface {
	Playback(ctx context.Context, t Target) (Playback, error)
}

// LiveMeta is a channel's current broadcast as the platform reports it.
type LiveMeta struct {
	LiveID     string // broadcast (episode) id; a new broadcast gets a new id
	Title      string
	Open       bool      // broadcasting now
	ChatRoom   string    // chat channel to connect to; empty when chat is unavailable
	StartedAt  time.Time // broadcast start; zero when unknown
	Viewers    int       // concurrent viewers; -1 when unknown
	ObservedAt time.Time
}

// LiveChat is one received message. AuthorID must not be stored (app pseudonymizes it).
type LiveChat struct {
	AuthorID   string
	Text       string
	MessageID  string    // platform message id, if any (used to drop replays after reconnects)
	EventTime  time.Time // platform's message time; zero when missing
	ReceivedAt time.Time
}

// LiveSink receives one connection's events in order. An error from Chat
// (e.g. the app's queue is full) ends the stream with that error.
type LiveSink interface {
	Connected(at time.Time)
	Chat(c LiveChat) error
	Heartbeat(at time.Time)
}

// LiveSource reads a broadcast's chat anonymously (read-only).
type LiveSource interface {
	LiveMeta(ctx context.Context, t Target) (LiveMeta, error)
	// LiveStream connects to room and delivers events until ctx ends
	// (returns ctx.Err()) or the connection fails. Failures are UserErrors;
	// LIVE_ACCESS, LIVE_SCHEMA and LIVE_CLOCK are permanent, others are worth a reconnect.
	LiveStream(ctx context.Context, t Target, room string, sink LiveSink) error
}

// LinkSource builds a link that opens the platform's own player at a position.
type LinkSource interface {
	WatchURL(t Target, offsetMs int64) string
}

// Registry holds the registered adapters.
type Registry struct {
	list []Platform
}

// Register adds adapters; IDs must be unique.
func (r *Registry) Register(ps ...Platform) {
	for _, p := range ps {
		for _, q := range r.list {
			if q.ID() == p.ID() {
				panic(fmt.Sprintf("platform %q registered twice", p.ID()))
			}
		}
		r.list = append(r.list, p)
	}
}

// Get returns the adapter with id.
func (r *Registry) Get(id string) (Platform, bool) {
	for _, p := range r.list {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

// All returns adapters in registration order.
func (r *Registry) All() []Platform { return append([]Platform(nil), r.list...) }

// Resolve finds the adapter for raw.
func (r *Registry) Resolve(raw string) (Platform, Target, error) {
	raw = strings.TrimSpace(raw)
	for _, p := range r.list {
		t, ok, err := p.Match(raw)
		if err != nil {
			return nil, Target{}, err
		}
		if ok {
			return p, t, nil
		}
	}
	names := make([]string, 0, len(r.list))
	for _, p := range r.list {
		names = append(names, p.Name())
	}
	return nil, Target{}, domain.NewUserError("INVALID_URL",
		"지원하는 플랫폼의 다시보기 주소를 입력해 주세요. 지원: "+strings.Join(names, ", "))
}

// MediaOrigins collects every adapter's media origins.
func (r *Registry) MediaOrigins() []string {
	var out []string
	for _, p := range r.list {
		out = append(out, p.MediaOrigins()...)
	}
	return out
}
