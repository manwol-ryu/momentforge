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

// LivePlaybackSource resolves the public stream of a channel's current
// broadcast. URLs may be signed; callers must not store them.
type LivePlaybackSource interface {
	LivePlayback(ctx context.Context, t Target) (Playback, error)
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

// Comment is a public comment under a VOD. AuthorID must not be stored (app
// pseudonymizes it). Hidden comments (deleted, secret, filtered by the
// platform) keep only their IDs.
type Comment struct {
	ID       string
	ParentID string // empty for top-level comments
	AuthorID string
	Text     string
	Hidden   bool
}

// CommentResult is what a comment fetch got. Issues name what is missing
// (page_budget, reply_incomplete, reply_stalled, or an error code after some
// comments were read); empty means the platform listed everything it reported.
type CommentResult struct {
	Comments      []Comment
	ReportedCount int // the platform's count of top-level comments, -1 unknown
	Pages         int
	Issues        []string
}

// CommentSource reads the public comments under a VOD, whose timestamps
// become viewer recommendations. It reads at most maxPages pages (replies
// included) and reports progress after each.
type CommentSource interface {
	VODComments(ctx context.Context, t Target, maxPages int, progress func(pages, comments int)) (CommentResult, error)
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

// AudioProgress is how far a VOD audio download has come.
type AudioProgress struct {
	Segments      int   // segments written
	TotalSegments int   // segments in the playlist (0 when unknown)
	Bytes         int64 // bytes written
	DoneMs        int64 // broadcast time covered so far
}

// AudioFile is a downloaded audio track for local transcription.
type AudioFile struct {
	Path       string // dst plus the container's extension (.mp4, .ts, .aac)
	DurationMs int64  // from the playlist
}

// VideoRequest asks for a VOD's video track.
type VideoRequest struct {
	// MaxHeight picks the tallest rendition at or below it (the shortest
	// rendition when none is); 0 takes the tallest.
	MaxHeight int
	// Resume continues a partial file at the download path; nil starts over.
	Resume *VideoResume
}

// VideoResume is where an interrupted video download stopped. The adapter
// starts over when the playlist no longer matches it.
type VideoResume struct {
	Height        int          `json:"height"`
	TotalSegments int          `json:"total_segments"`
	Segments      int          `json:"segments"`  // segments already in the file
	Bytes         int64        `json:"bytes"`     // the file's length after them
	Fragments     uint32       `json:"fragments"` // movie fragments written (the next one is numbered Fragments+1)
	DoneMs        int64        `json:"done_ms"`
	Shift         []TrackShift `json:"shift"`
	Missing       []domain.Gap `json:"missing"`
}

// TrackShift is how far a track's timestamps were moved back so the file
// starts at 0 (in the track's timescale).
type TrackShift struct {
	TrackID   uint32 `json:"track_id"`
	Timescale uint32 `json:"timescale"`
	Ticks     uint64 `json:"ticks"`
}

// VideoRendition is the variant a video download took.
type VideoRendition struct {
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Bandwidth int64   `json:"bandwidth"`
	Codecs    string  `json:"codecs"`
	FrameRate float64 `json:"frame_rate"`
}

// VideoProgress is a video download's state after each segment; State is
// enough to resume from.
type VideoProgress struct {
	Rendition VideoRendition
	State     VideoResume
}

// VideoFile is a finished video download.
type VideoFile struct {
	Path       string
	DurationMs int64 // from the playlist
	Container  string
	Rendition  VideoRendition
	// FirstPTSMs is the stream's first presentation time before the file
	// was rebased to start at 0 (docs/AI-CUT-ANALYSIS.md 2.5).
	FirstPTSMs int64
	State      VideoResume // segments, bytes, shift and missing spans of the whole file
}

// VideoSource downloads a VOD's public stream into one fragmented MP4
// without re-encoding: the init section and every segment in order, with
// segment timestamps moved back so the file's 0 s is the video's 0 s. It is
// the session backup bundle's editing copy (docs/AI-CUT-ANALYSIS.md 2.5).
type VideoSource interface {
	VODVideo(ctx context.Context, t Target, path string, req VideoRequest, progress func(VideoProgress)) (VideoFile, error)
}

// AudioSource downloads the lightest rendition of a VOD's public stream
// (audio-only when the platform offers one) into a file at dst plus an
// extension, so it can be transcribed on this computer
// (docs/AI-CUT-ANALYSIS.md 2.3). The file is temporary: the caller deletes
// it after transcribing. Nothing is stored or played from it.
type AudioSource interface {
	VODAudio(ctx context.Context, t Target, dst string, progress func(AudioProgress)) (AudioFile, error)
}

// LiveAudioSegment is one piece of a broadcast's audio recorded while a
// LIVE capture runs.
type LiveAudioSegment struct {
	Path  string    // the segment's own file
	Init  string    // the init section's file it needs (fragmented MP4), "" for MPEG-TS or packed audio
	At    time.Time // when it starts: the playlist's program date-time, else when it appeared less its length
	DurMs int64
}

// LiveAudioSource records the lightest rendition of a channel's current
// broadcast (audio-only when the platform offers one) while a LIVE capture
// runs, one file per segment in dir, so the app can transcribe its
// candidates on this computer. RecordLiveAudio returns when ctx ends, the
// broadcast ends (nil) or the stream stays unreachable (an error).
// AssembleAudio joins recorded segments of one unbroken stretch, in order,
// into one playable file at dst plus an extension, starting at 0 s. The
// files are temporary: the app deletes them when the capture ends, and
// never plays or keeps them.
type LiveAudioSource interface {
	RecordLiveAudio(ctx context.Context, t Target, dir string, onSegment func(LiveAudioSegment)) error
	AssembleAudio(segs []LiveAudioSegment, dst string) (AudioFile, error)
}
