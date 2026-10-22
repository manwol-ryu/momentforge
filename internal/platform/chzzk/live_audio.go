package chzzk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// Limits of a live audio recording.
const (
	liveAudioFails   = 10               // playlist failures in a row before giving up
	liveAudioMaxByte = int64(16 << 30)  // a 24-hour broadcast at 144p is well below
	liveAudioPoll    = 2 * time.Second  // how often the playlist is read when it says nothing
	liveAudioMaxPoll = 6 * time.Second  // and at most, whatever its target duration
	liveAudioBackoff = 30 * time.Second // the longest wait after a failure
	liveAudioRefresh = 20 * time.Minute // the signed playback URL is fetched again this often
)

// RecordLiveAudio records the channel's current broadcast, the lightest
// rendition, a segment per file in dir, until ctx ends or the broadcast
// does (docs/REDESIGN.md 15, 2026-10-22). The signed stream URL is fetched
// fresh, again when it stops answering, and never stored.
func (c *Chzzk) RecordLiveAudio(ctx context.Context, t platform.Target, dir string, onSegment func(platform.LiveAudioSegment)) error {
	resolve := func(ctx context.Context) (string, error) {
		pb, err := c.LivePlayback(ctx, t)
		return pb.URL, err
	}
	return recordHLS(ctx, newLiveAudioClient(), resolve, dir, allowedLiveMedia, onSegment, time.Now, sleepCtx)
}

// newLiveAudioClient is a CDN client of the recording's own: a live
// segment stays in the playlist for seconds, so retries are few and short.
func newLiveAudioClient() *platform.HTTPClient {
	return &platform.HTTPClient{
		Header:   http.Header{"User-Agent": {"Mozilla/5.0"}, "Referer": {webOrigin + "/"}},
		Attempts: 3,
		MaxBytes: 16 << 20,
		MaxWait:  5 * time.Second,
		Timeout:  20 * time.Second,
	}
}

// AssembleAudio joins recorded segments into one file starting at 0 s.
func (c *Chzzk) AssembleAudio(segs []platform.LiveAudioSegment, dst string) (platform.AudioFile, error) {
	return assembleAudio(segs, dst)
}

// allowedLiveMedia is allowedMedia for a LIVE stream, whose CDNs rotate (chzzk.go).
func allowedLiveMedia(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443") &&
		(hasSuffix(u.Hostname(), cdnSuffix) || hasSuffix(u.Hostname(), liveCDNSuffix))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type liveSegment struct {
	url   *url.URL
	seq   int64
	durMs int64
	at    time.Time // EXT-X-PROGRAM-DATE-TIME, zero when the playlist has none
}

type livePlaylist struct {
	init     *url.URL
	segments []liveSegment
	target   time.Duration // EXT-X-TARGETDURATION
	ended    bool
}

// parseLivePlaylist reads a live media playlist: its media sequence, init
// section, segments with their durations and program date-times.
func parseLivePlaylist(body string, base *url.URL) (livePlaylist, error) {
	var pl livePlaylist
	seq := int64(0)
	var pending int64 = -1
	var at time.Time
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			n, err := strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
			if err != nil || n < 0 {
				return pl, schema("생방송 재생 목록의 순번을 읽지 못했습니다.")
			}
			seq = n
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			if secs, err := strconv.ParseFloat(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"), 64); err == nil && secs > 0 {
				pl.target = time.Duration(secs * float64(time.Second))
			}
		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			if t, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:")); err == nil {
				at = t
			}
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			attrs := parseAttrs(strings.TrimPrefix(line, "#EXT-X-MAP:"))
			ref, err := base.Parse(attrs["URI"])
			if attrs["URI"] == "" || err != nil {
				return pl, schema("생방송 재생 목록의 초기화 조각 주소를 읽지 못했습니다.")
			}
			if pl.init == nil {
				pl.init = ref
			}
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			attrs := parseAttrs(strings.TrimPrefix(line, "#EXT-X-KEY:"))
			if attrs["METHOD"] != "" && attrs["METHOD"] != "NONE" {
				return pl, domain.NewUserError("PLAYBACK_RESTRICTED", "이 방송의 음성은 암호화되어 있어 받지 않습니다.")
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			dur, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			secs, err := strconv.ParseFloat(strings.TrimSpace(dur), 64)
			if err != nil || secs < 0 {
				return pl, schema("생방송 재생 목록의 조각 길이를 읽지 못했습니다.")
			}
			pending = int64(secs * 1000)
		case line == "#EXT-X-ENDLIST":
			pl.ended = true
		case strings.HasPrefix(line, "#"):
		default:
			if pending < 0 {
				continue
			}
			ref, err := base.Parse(line)
			if err != nil {
				return pl, schema("생방송 재생 목록의 주소를 읽지 못했습니다.")
			}
			pl.segments = append(pl.segments, liveSegment{url: ref, seq: seq, durMs: pending, at: at})
			if !at.IsZero() {
				at = at.Add(time.Duration(pending) * time.Millisecond)
			}
			seq++
			pending = -1
		}
	}
	return pl, nil
}

// recordHLS follows a live stream: the media playlist of the lightest
// rendition is read again and again, and each new segment is written to
// its own file in dir and reported. A segment's start is its program
// date-time when the playlist has one, else when it appeared less what
// follows it in the playlist. resolve gives the stream's current (signed)
// URL; it is asked again when the playlist stops answering and every
// liveAudioRefresh. now and sleep are the clock (tests replace them).
func recordHLS(ctx context.Context, client *platform.HTTPClient, resolve func(context.Context) (string, error), dir string,
	allow func(*url.URL) bool, onSegment func(platform.LiveAudioSegment), now func() time.Time,
	sleep func(context.Context, time.Duration) error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var media *url.URL
	var resolved time.Time
	initPath, lastSeq, fails := "", int64(-1), 0
	var written int64
	// A failure waits longer each time and asks for the (signed) URL again;
	// a cancelled ctx ends the loop at its top.
	fail := func(err error) error {
		if fails++; fails >= liveAudioFails {
			return err
		}
		media = nil
		_ = sleep(ctx, min(liveAudioBackoff, time.Duration(fails)*liveAudioPoll))
		return nil
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		if media == nil || now().Sub(resolved) > liveAudioRefresh {
			raw, err := resolve(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				ue, _ := domain.AsUserError(err)
				switch {
				case ue != nil && (ue.Code == "LIVE_OFFLINE" || ue.Code == "LIVE_MISSING"):
					return nil // the broadcast ended
				case ue != nil && ue.Code != "LIVE_NETWORK" && ue.Code != "LIVE_RATE":
					return err // restricted or unavailable: asking again changes nothing
				}
				if err := fail(err); err != nil {
					return err
				}
				continue
			}
			first, err := url.Parse(raw)
			if err != nil || !allow(first) {
				return domain.NewUserError("PLAYBACK_UNAVAILABLE", "방송 주소가 허용된 CDN이 아니어서 음성을 받지 않았습니다.")
			}
			body, err := fetchText(ctx, client, first, allow)
			if err != nil {
				if err := fail(err); err != nil {
					return err
				}
				continue
			}
			u, isMaster, err := pickRendition(body, first)
			if err != nil {
				return err
			}
			if isMaster && !allow(u) {
				return schema("재생 목록이 허용되지 않은 주소를 가리킵니다.")
			}
			media, resolved = u, now()
		}
		fetched := now()
		body, err := fetchText(ctx, client, media, allow)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err := fail(err); err != nil {
				return err
			}
			continue
		}
		pl, err := parseLivePlaylist(body, media)
		if err != nil {
			return err
		}
		fails = 0
		if n := len(pl.segments); n > 0 && pl.segments[n-1].seq+int64(n) < lastSeq {
			lastSeq = -1 // the stream restarted its numbering
		}
		if pl.init != nil && initPath == "" {
			if !allow(pl.init) {
				return schema("재생 목록이 허용되지 않은 주소를 가리킵니다.")
			}
			initPath = filepath.Join(dir, "init"+path.Ext(pl.init.Path))
			if _, err := fetchTo(ctx, client, pl.init, initPath); err != nil {
				return err
			}
		}
		// Without program date-times, a segment started before the time of
		// this read by the length of itself and of the segments after it.
		after := int64(0)
		for _, s := range pl.segments {
			after += s.durMs
		}
		for _, s := range pl.segments {
			if s.seq <= lastSeq {
				after -= s.durMs
				continue
			}
			if !allow(s.url) {
				return schema("재생 목록이 허용되지 않은 주소를 가리킵니다.")
			}
			at := s.at
			if at.IsZero() {
				at = fetched.Add(-time.Duration(after) * time.Millisecond)
			}
			after -= s.durMs
			p := filepath.Join(dir, fmt.Sprintf("%012d%s", s.seq, segmentExt(s.url)))
			n, err := fetchTo(ctx, client, s.url, p)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				if errors.Is(err, errSegmentGone) {
					lastSeq = s.seq // it left the CDN before it was read: a gap the app sees
					continue
				}
				return err
			}
			if written += n; written > liveAudioMaxByte {
				return domain.NewUserError("MEDIA", "방송 음성이 예상 크기를 넘어 받기를 멈췄습니다.")
			}
			lastSeq = s.seq
			if onSegment != nil {
				onSegment(platform.LiveAudioSegment{Path: p, Init: initPath, At: at, DurMs: s.durMs})
			}
		}
		if pl.ended {
			return nil
		}
		wait := liveAudioPoll
		if pl.target > 0 {
			wait = min(liveAudioMaxPoll, pl.target)
		}
		if err := sleep(ctx, wait); err != nil {
			return nil
		}
	}
}

var errSegmentGone = errors.New("segment gone")

// fetchTo writes one URL's body to a new file (0600) and returns its size.
func fetchTo(ctx context.Context, client *platform.HTTPClient, u *url.URL, dst string) (int64, error) {
	res, err := client.Get(ctx, u.String())
	if err != nil {
		return 0, err
	}
	switch {
	case res.Status == 404 || res.Status == 410:
		return 0, errSegmentGone
	case res.Status != 200:
		return 0, domain.NewUserError("HTTP_ERROR", fmt.Sprintf("방송 음성 조각을 받지 못했습니다 (HTTP %d).", res.Status))
	}
	if err := os.WriteFile(dst, res.Body, 0o600); err != nil {
		return 0, err
	}
	return int64(len(res.Body)), nil
}

// segmentExt keeps a segment's own extension (.ts, .aac, .m4s), .ts when it has none.
func segmentExt(u *url.URL) string {
	if ext := strings.ToLower(path.Ext(u.Path)); ext != "" && len(ext) <= 5 {
		return ext
	}
	return ".ts"
}

// assembleAudio writes segments, in order, as one file at dst plus the
// container's extension: fragmented MP4 (init section, then the fragments
// rebased so the file starts at 0 s and renumbered), packed AAC or MPEG-TS
// (joined as they are; their own timestamps restart where readers expect).
func assembleAudio(segs []platform.LiveAudioSegment, dst string) (platform.AudioFile, error) {
	if len(segs) == 0 {
		return platform.AudioFile{}, domain.NewUserError("INPUT", "받은 방송 음성이 없습니다.")
	}
	ext := strings.ToLower(filepath.Ext(segs[0].Path))
	fmp4 := segs[0].Init != "" || ext == ".m4s" || ext == ".mp4" || ext == ".m4a"
	out := dst + ".ts"
	switch {
	case fmp4:
		out = dst + ".mp4"
	case ext == ".aac":
		out = dst + ".aac"
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return platform.AudioFile{}, err
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return platform.AudioFile{}, fmt.Errorf("create audio file: %w", err)
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(out)
		}
	}()
	var timescales map[uint32]uint32
	var shift []platform.TrackShift
	var seq uint32
	if segs[0].Init != "" {
		b, err := os.ReadFile(segs[0].Init)
		if err != nil {
			return platform.AudioFile{}, err
		}
		timescales, _ = initTimescales(b)
		if _, err := f.Write(b); err != nil {
			return platform.AudioFile{}, err
		}
	}
	var total int64
	for _, s := range segs {
		b, err := os.ReadFile(s.Path)
		if err != nil {
			return platform.AudioFile{}, err
		}
		if timescales != nil {
			if shift == nil {
				if shift, _, err = chooseShift(b, timescales); err != nil {
					return platform.AudioFile{}, schema("방송 음성 조각의 형식을 읽지 못했습니다.")
				}
			}
			if b, err = rebaseSegment(b, shift, &seq); err != nil {
				return platform.AudioFile{}, schema("방송 음성 조각의 형식을 읽지 못했습니다.")
			}
		}
		if _, err := f.Write(b); err != nil {
			return platform.AudioFile{}, err
		}
		total += s.DurMs
	}
	if err := f.Sync(); err != nil {
		return platform.AudioFile{}, err
	}
	ok = true
	return platform.AudioFile{Path: out, DurationMs: total}, nil
}
