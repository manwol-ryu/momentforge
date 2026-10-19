package chzzk

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// maxVideoByte bounds one video download (a day-long 1080p broadcast is below it).
const maxVideoByte = 96 << 30

// VODVideo downloads the VOD's public HLS stream at the rendition the
// request asks for into one fragmented MP4 at path, rebased to start at
// 0 s (docs/AI-CUT-ANALYSIS.md 2.5). The signed playback URL is fetched
// fresh and not stored; a resumed download fetches it again.
func (c *Chzzk) VODVideo(ctx context.Context, t platform.Target, path string, req platform.VideoRequest,
	progress func(platform.VideoProgress)) (platform.VideoFile, error) {
	pb, err := c.Playback(ctx, t)
	if err != nil {
		return platform.VideoFile{}, err
	}
	return downloadVideo(ctx, c.cdn, pb.URL, path, req, allowedMedia, progress)
}

// variant is one EXT-X-STREAM-INF entry.
type variant struct {
	url       *url.URL
	rendition platform.VideoRendition
}

// pickVideo reads a master playlist and returns the tallest variant at or
// below maxHeight (the shortest variant when none is; the tallest when
// maxHeight is 0). Variants without a resolution are skipped.
func pickVideo(body string, base *url.URL, maxHeight int) (variant, error) {
	var list []variant
	var pending *platform.VideoRendition
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			attrs := parseAttrs(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
			r := platform.VideoRendition{Codecs: attrs["CODECS"]}
			r.Bandwidth, _ = strconv.ParseInt(attrs["BANDWIDTH"], 10, 64)
			r.FrameRate, _ = strconv.ParseFloat(attrs["FRAME-RATE"], 64)
			if w, h, ok := strings.Cut(attrs["RESOLUTION"], "x"); ok {
				r.Width, _ = strconv.Atoi(w)
				r.Height, _ = strconv.Atoi(h)
			}
			pending = &r
		case strings.HasPrefix(line, "#"):
			continue
		default:
			if pending == nil {
				continue
			}
			ref, err := base.Parse(line)
			if err != nil {
				return variant{}, schema("재생 목록의 주소를 읽지 못했습니다.")
			}
			if pending.Height > 0 {
				list = append(list, variant{url: ref, rendition: *pending})
			}
			pending = nil
		}
	}
	if len(list) == 0 {
		return variant{}, schema("재생 목록에서 영상 화질 목록을 찾지 못했습니다.")
	}
	var best, shortest *variant
	for i := range list {
		v := &list[i]
		if shortest == nil || v.rendition.Height < shortest.rendition.Height {
			shortest = v
		}
		if maxHeight > 0 && v.rendition.Height > maxHeight {
			continue
		}
		if best == nil || v.rendition.Height > best.rendition.Height ||
			(v.rendition.Height == best.rendition.Height && v.rendition.Bandwidth > best.rendition.Bandwidth) {
			best = v
		}
	}
	if best == nil {
		best = shortest
	}
	return *best, nil
}

// downloadVideo fetches the master playlist, picks the variant, and writes
// its init section and every segment, rebased, to path. Segments the CDN
// answers 404 or 410 for are left out and reported as missing spans; any
// other failure stops the download, which can resume from the last
// progress state.
func downloadVideo(ctx context.Context, client *platform.HTTPClient, playlistURL, path string, req platform.VideoRequest,
	allow func(*url.URL) bool, progress func(platform.VideoProgress)) (platform.VideoFile, error) {
	first, err := url.Parse(playlistURL)
	if err != nil || !allow(first) {
		return platform.VideoFile{}, domain.NewUserError("PLAYBACK_UNAVAILABLE", "재생 주소가 허용된 CDN이 아니어서 영상을 받지 않았습니다.")
	}
	body, err := fetchText(ctx, client, first, allow)
	if err != nil {
		return platform.VideoFile{}, err
	}
	v, err := pickVideo(body, first, req.MaxHeight)
	if err != nil {
		return platform.VideoFile{}, err
	}
	if !allow(v.url) {
		return platform.VideoFile{}, schema("재생 목록이 허용되지 않은 주소를 가리킵니다.")
	}
	if body, err = fetchText(ctx, client, v.url, allow); err != nil {
		return platform.VideoFile{}, err
	}
	pl, err := parseMediaPlaylist(body, v.url)
	if err != nil {
		return platform.VideoFile{}, err
	}
	switch {
	case len(pl.segments) == 0:
		return platform.VideoFile{}, schema("재생 목록에 영상 조각이 없습니다.")
	case !pl.ended:
		return platform.VideoFile{}, domain.NewUserError("MEDIA", "재생 목록이 아직 끝나지 않았습니다. 방송이 끝나고 다시보기가 준비된 뒤 받아 주세요.")
	case pl.init == nil:
		return platform.VideoFile{}, domain.NewUserError("MEDIA",
			"이 영상의 조각 형식(MPEG-TS)은 재인코딩 없이 MP4로 담을 수 없어 받지 않았습니다. 영상 없이 묶음을 만들 수 있습니다.")
	case len(pl.segments) > maxSegments:
		return platform.VideoFile{}, domain.NewUserError("MEDIA", "재생 목록의 조각 수가 너무 많아 받지 않았습니다.")
	}
	if !allow(pl.init) {
		return platform.VideoFile{}, schema("재생 목록이 허용되지 않은 주소를 가리킵니다.")
	}
	for _, s := range pl.segments {
		if !allow(s.url) {
			return platform.VideoFile{}, schema("재생 목록이 허용되지 않은 주소를 가리킵니다.")
		}
	}
	var total int64
	for _, s := range pl.segments {
		total += s.durMs
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return platform.VideoFile{}, err
	}

	get := func(u *url.URL) ([]byte, int, error) {
		res, err := client.Get(ctx, u.String())
		if err != nil {
			return nil, 0, err
		}
		return res.Body, res.Status, nil
	}
	initBody, status, err := get(pl.init)
	if err != nil {
		return platform.VideoFile{}, err
	}
	if status != 200 {
		return platform.VideoFile{}, domain.NewUserError("HTTP_ERROR", fmt.Sprintf("영상 초기화 조각을 받지 못했습니다 (HTTP %d).", status))
	}
	timescales, err := initTimescales(initBody)
	if err != nil {
		return platform.VideoFile{}, schema("영상 초기화 조각의 형식을 읽지 못했습니다.")
	}

	// Resume only a partial file of the same playlist whose length is at least what was recorded.
	st := platform.VideoResume{Height: v.rendition.Height, TotalSegments: len(pl.segments)}
	flags := os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	if r := req.Resume; r != nil && r.Height == st.Height && r.TotalSegments == st.TotalSegments &&
		r.Segments > 0 && r.Segments <= len(pl.segments) && len(r.Shift) > 0 && r.Fragments > 0 {
		if info, err := os.Stat(path); err == nil && info.Size() >= r.Bytes {
			st = *r
			st.Missing = append([]domain.Gap(nil), r.Missing...)
			flags = os.O_WRONLY
		}
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return platform.VideoFile{}, fmt.Errorf("create video file: %w", err)
	}
	defer f.Close()
	if flags&os.O_TRUNC == 0 {
		if err := f.Truncate(st.Bytes); err != nil {
			return platform.VideoFile{}, fmt.Errorf("resume video file: %w", err)
		}
		if _, err := f.Seek(st.Bytes, 0); err != nil {
			return platform.VideoFile{}, fmt.Errorf("resume video file: %w", err)
		}
	} else {
		if _, err := f.Write(initBody); err != nil {
			return platform.VideoFile{}, fmt.Errorf("write video file: %w", err)
		}
		st.Bytes = int64(len(initBody))
	}

	for i := st.Segments; i < len(pl.segments); i++ {
		s := pl.segments[i]
		seg, status, err := get(s.url)
		if err != nil {
			return platform.VideoFile{}, err
		}
		switch {
		case status == 404 || status == 410:
			st.Missing = appendGap(st.Missing, domain.Gap{StartMs: st.DoneMs, EndMs: st.DoneMs + s.durMs})
			seg = nil
		case status != 200:
			return platform.VideoFile{}, domain.NewUserError("HTTP_ERROR", fmt.Sprintf("영상 조각을 받지 못했습니다 (HTTP %d).", status))
		case len(st.Shift) == 0:
			shift, _, err := chooseShift(seg, timescales)
			if err != nil {
				return platform.VideoFile{}, schema("영상 조각의 형식을 읽지 못했습니다.")
			}
			// When the first segments are missing, this one still sits at its playlist time.
			for k := range shift {
				back := uint64(st.DoneMs) * uint64(shift[k].Timescale) / 1000
				shift[k].Ticks -= min(back, shift[k].Ticks)
			}
			st.Shift = shift
		}
		if seg != nil {
			out, err := rebaseSegment(seg, st.Shift, &st.Fragments)
			if err != nil {
				return platform.VideoFile{}, schema("영상 조각의 형식을 읽지 못했습니다.")
			}
			if st.Bytes+int64(len(out)) > maxVideoByte {
				return platform.VideoFile{}, domain.NewUserError("MEDIA", "영상 크기가 예상 범위를 넘어 받기를 멈췄습니다.")
			}
			if _, err := f.Write(out); err != nil {
				return platform.VideoFile{}, fmt.Errorf("write video file: %w", err)
			}
			st.Bytes += int64(len(out))
		}
		st.Segments = i + 1
		st.DoneMs += s.durMs
		if progress != nil {
			progress(platform.VideoProgress{Rendition: v.rendition, State: st})
		}
	}
	if len(st.Missing) > 0 && len(st.Shift) == 0 {
		return platform.VideoFile{}, domain.NewUserError("MEDIA", "영상 조각을 하나도 받지 못했습니다.")
	}
	if err := f.Sync(); err != nil {
		return platform.VideoFile{}, err
	}
	return platform.VideoFile{Path: path, DurationMs: total, Container: "fmp4", Rendition: v.rendition,
		FirstPTSMs: shiftMs(st.Shift), State: st}, nil
}

// shiftMs is the rebase in milliseconds (the earliest track's, which is the smallest).
func shiftMs(shift []platform.TrackShift) int64 {
	var out int64 = -1
	for _, s := range shift {
		if ms := int64(s.Ticks * 1000 / uint64(s.Timescale)); out < 0 || ms < out {
			out = ms
		}
	}
	return out
}

// appendGap adds g, merging it into the last span when they touch.
func appendGap(list []domain.Gap, g domain.Gap) []domain.Gap {
	if n := len(list); n > 0 && list[n-1].EndMs >= g.StartMs {
		list[n-1].EndMs = max(list[n-1].EndMs, g.EndMs)
		return list
	}
	return append(list, g)
}

var _ platform.VideoSource = (*Chzzk)(nil)
