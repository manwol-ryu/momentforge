package chzzk

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// Limits of one audio download (a day-long broadcast is far below them).
const (
	maxSegments  = 200_000
	maxAudioByte = 8 << 30
)

// VODAudio downloads the lightest rendition of the VOD's public HLS stream
// (an audio-only rendition when the master playlist offers one, else the
// lowest-bandwidth variant) to dst plus the container's extension
// (docs/AI-CUT-ANALYSIS.md 2.3). Every URL must be https on the CDN; the
// signed playback URL is fetched fresh and not stored.
func (c *Chzzk) VODAudio(ctx context.Context, t platform.Target, dst string, progress func(platform.AudioProgress)) (platform.AudioFile, error) {
	pb, err := c.Playback(ctx, t)
	if err != nil {
		return platform.AudioFile{}, err
	}
	return downloadHLS(ctx, c.cdn, pb.URL, dst, allowedMedia, progress)
}

// allowedMedia is publicHLS's rule for one URL: https, no credentials, port 443, a CDN host.
func allowedMedia(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443") && hasSuffix(u.Hostname(), cdnSuffix)
}

// newCDNClient is the polite client for media: no pacing between segments
// (a CDN serves them as a player would), retries like the API client, and a
// larger size cap.
func newCDNClient() *platform.HTTPClient {
	return &platform.HTTPClient{
		Header:   http.Header{"User-Agent": {"Mozilla/5.0"}, "Referer": {webOrigin + "/"}},
		Attempts: 5,
		MaxBytes: 64 << 20,
		MaxWait:  300 * time.Second,
		Timeout:  60 * time.Second,
	}
}

type segment struct {
	url   *url.URL
	durMs int64
}

type mediaPlaylist struct {
	init     *url.URL // EXT-X-MAP (fMP4), nil for TS or packed audio
	segments []segment
	ended    bool
}

// downloadHLS fetches the playlist at playlistURL (a master or a media
// playlist), picks the lightest audio rendition, and writes the init
// section and every segment in order to one file. allow decides which
// URLs may be fetched; everything else is refused before any request.
func downloadHLS(ctx context.Context, client *platform.HTTPClient, playlistURL, dst string, allow func(*url.URL) bool,
	progress func(platform.AudioProgress)) (platform.AudioFile, error) {
	first, err := url.Parse(playlistURL)
	if err != nil || !allow(first) {
		return platform.AudioFile{}, domain.NewUserError("PLAYBACK_UNAVAILABLE", "재생 주소가 허용된 CDN이 아니어서 음성을 받지 않았습니다.")
	}
	body, err := fetchText(ctx, client, first, allow)
	if err != nil {
		return platform.AudioFile{}, err
	}
	mediaURL, isMaster, err := pickRendition(body, first)
	if err != nil {
		return platform.AudioFile{}, err
	}
	if isMaster {
		if !allow(mediaURL) {
			return platform.AudioFile{}, schema("재생 목록이 허용되지 않은 주소를 가리킵니다.")
		}
		if body, err = fetchText(ctx, client, mediaURL, allow); err != nil {
			return platform.AudioFile{}, err
		}
	}
	pl, err := parseMediaPlaylist(body, mediaURL)
	if err != nil {
		return platform.AudioFile{}, err
	}
	if len(pl.segments) == 0 {
		return platform.AudioFile{}, schema("재생 목록에 음성 조각이 없습니다.")
	}
	if len(pl.segments) > maxSegments {
		return platform.AudioFile{}, domain.NewUserError("MEDIA", "재생 목록의 조각 수가 너무 많아 받지 않았습니다.")
	}
	for _, s := range pl.segments {
		if !allow(s.url) {
			return platform.AudioFile{}, schema("재생 목록이 허용되지 않은 주소를 가리킵니다.")
		}
	}
	if pl.init != nil && !allow(pl.init) {
		return platform.AudioFile{}, schema("재생 목록이 허용되지 않은 주소를 가리킵니다.")
	}
	var total int64
	for _, s := range pl.segments {
		total += s.durMs
	}
	out := dst + containerExt(pl)
	if err := os.MkdirAll(path.Dir(out), 0o700); err != nil {
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
	var written int64
	write := func(u *url.URL) error {
		res, err := client.Get(ctx, u.String())
		if err != nil {
			return err
		}
		if res.Status != 200 {
			return domain.NewUserError("HTTP_ERROR", fmt.Sprintf("음성 조각을 받지 못했습니다 (HTTP %d).", res.Status))
		}
		if written += int64(len(res.Body)); written > maxAudioByte {
			return domain.NewUserError("MEDIA", "음성 크기가 예상 범위를 넘어 받기를 멈췄습니다.")
		}
		_, err = f.Write(res.Body)
		return err
	}
	if pl.init != nil {
		if err := write(pl.init); err != nil {
			return platform.AudioFile{}, err
		}
	}
	var done int64
	for i, s := range pl.segments {
		if err := write(s.url); err != nil {
			return platform.AudioFile{}, err
		}
		done += s.durMs
		if progress != nil {
			progress(platform.AudioProgress{Segments: i + 1, TotalSegments: len(pl.segments), Bytes: written, DoneMs: done})
		}
	}
	if err := f.Sync(); err != nil {
		return platform.AudioFile{}, err
	}
	ok = true
	return platform.AudioFile{Path: out, DurationMs: total}, nil
}

// fetchText GETs a playlist; redirects must stay on allowed hosts.
func fetchText(ctx context.Context, client *platform.HTTPClient, u *url.URL, allow func(*url.URL) bool) (string, error) {
	client.AllowedHost = u.Hostname()
	res, err := client.Get(ctx, u.String())
	if err != nil {
		return "", err
	}
	if res.Status != 200 {
		return "", domain.NewUserError("HTTP_ERROR", fmt.Sprintf("재생 목록을 받지 못했습니다 (HTTP %d).", res.Status))
	}
	body := string(res.Body)
	if !strings.HasPrefix(strings.TrimSpace(body), "#EXTM3U") {
		return "", schema("재생 목록(m3u8) 형식이 아닙니다.")
	}
	return body, nil
}

// pickRendition reads a master playlist and returns the media playlist to
// fetch: an audio-only rendition (EXT-X-MEDIA TYPE=AUDIO with a URI,
// DEFAULT=YES first) when present, else the lowest-BANDWIDTH variant. When
// the body is already a media playlist (it has segments), it returns base
// with isMaster false.
func pickRendition(body string, base *url.URL) (u *url.URL, isMaster bool, err error) {
	var audio, audioDefault *url.URL
	var lowest *url.URL
	lowestBW := int64(-1)
	var pendingBW int64 = -1
	hasSegments := false
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || (strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#EXT")):
			continue
		case strings.HasPrefix(line, "#EXTINF:"):
			hasSegments = true
		case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
			attrs := parseAttrs(strings.TrimPrefix(line, "#EXT-X-MEDIA:"))
			if attrs["TYPE"] != "AUDIO" || attrs["URI"] == "" {
				continue
			}
			ref, err := base.Parse(attrs["URI"])
			if err != nil {
				return nil, false, schema("재생 목록의 주소를 읽지 못했습니다.")
			}
			if attrs["DEFAULT"] == "YES" && audioDefault == nil {
				audioDefault = ref
			}
			if audio == nil {
				audio = ref
			}
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			attrs := parseAttrs(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
			pendingBW, _ = strconv.ParseInt(attrs["BANDWIDTH"], 10, 64)
			if pendingBW <= 0 {
				pendingBW = 1<<62 - 1 // unknown bandwidth sorts last
			}
		case strings.HasPrefix(line, "#"):
			continue
		default: // a URI line
			if pendingBW < 0 {
				continue
			}
			ref, err := base.Parse(line)
			if err != nil {
				return nil, false, schema("재생 목록의 주소를 읽지 못했습니다.")
			}
			if lowestBW < 0 || pendingBW < lowestBW {
				lowest, lowestBW = ref, pendingBW
			}
			pendingBW = -1
		}
	}
	switch {
	case audioDefault != nil:
		return audioDefault, true, nil
	case audio != nil:
		return audio, true, nil
	case lowest != nil:
		return lowest, true, nil
	case hasSegments:
		return base, false, nil
	}
	return nil, false, schema("재생 목록에서 음성 렌디션을 찾지 못했습니다.")
}

// parseMediaPlaylist reads the init section and the segments with their durations.
func parseMediaPlaylist(body string, base *url.URL) (mediaPlaylist, error) {
	var pl mediaPlaylist
	var pending int64 = -1
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			attrs := parseAttrs(strings.TrimPrefix(line, "#EXT-X-MAP:"))
			if attrs["URI"] == "" {
				return pl, schema("재생 목록의 초기화 조각 주소가 없습니다.")
			}
			ref, err := base.Parse(attrs["URI"])
			if err != nil {
				return pl, schema("재생 목록의 주소를 읽지 못했습니다.")
			}
			if pl.init == nil {
				pl.init = ref
			}
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			attrs := parseAttrs(strings.TrimPrefix(line, "#EXT-X-KEY:"))
			if attrs["METHOD"] != "" && attrs["METHOD"] != "NONE" {
				return pl, domain.NewUserError("PLAYBACK_RESTRICTED", "이 영상의 음성은 암호화되어 있어 받지 않습니다.")
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			dur, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			secs, err := strconv.ParseFloat(strings.TrimSpace(dur), 64)
			if err != nil || secs < 0 {
				return pl, schema("재생 목록의 조각 길이를 읽지 못했습니다.")
			}
			pending = int64(secs * 1000)
		case line == "#EXT-X-ENDLIST":
			pl.ended = true
		case strings.HasPrefix(line, "#"):
			continue
		default:
			if pending < 0 {
				continue
			}
			ref, err := base.Parse(line)
			if err != nil {
				return pl, schema("재생 목록의 주소를 읽지 못했습니다.")
			}
			pl.segments = append(pl.segments, segment{url: ref, durMs: pending})
			pending = -1
		}
	}
	return pl, nil
}

// containerExt names the file by what the segments are: fragmented MP4
// (an init section or .m4s/.mp4 parts), packed AAC, or MPEG-TS.
func containerExt(pl mediaPlaylist) string {
	ext := strings.ToLower(path.Ext(pl.segments[0].url.Path))
	switch {
	case pl.init != nil, ext == ".m4s", ext == ".mp4", ext == ".m4a":
		return ".mp4"
	case ext == ".aac":
		return ".aac"
	}
	return ".ts"
}

// parseAttrs reads an HLS attribute list (KEY=value,KEY="quoted, value").
func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 {
		k, rest, ok := strings.Cut(s, "=")
		if !ok {
			break
		}
		k = strings.TrimSpace(k)
		var v string
		if strings.HasPrefix(rest, `"`) {
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				v, rest = rest[1:], ""
			} else {
				v, rest = rest[1:1+end], rest[2+end:]
				rest = strings.TrimPrefix(rest, ",")
			}
		} else {
			v, rest, _ = strings.Cut(rest, ",")
		}
		out[k] = v
		s = rest
	}
	return out
}

var _ platform.AudioSource = (*Chzzk)(nil)
