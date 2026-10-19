//go:build e2efake

package e2efake

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// fakeVideoSegments is how many 30-second "segments" the fake VOD's video has.
const fakeVideoSegments = 40

// VODVideo writes the synthetic VOD's "video": placeholder bytes per
// segment (not a playable file), slowly enough that the bundle's progress
// and cancel can be watched, resuming from req.Resume like the real
// adapter. Video "noaudio" has no stream.
func (f *Fake) VODVideo(ctx context.Context, t platform.Target, path string, req platform.VideoRequest,
	progress func(platform.VideoProgress)) (platform.VideoFile, error) {
	if t.ID == "noaudio" {
		return platform.VideoFile{}, domain.NewUserError("PLAYBACK_UNAVAILABLE", "공개 재생 주소를 확인하지 못했습니다. 이 영상은 치지직에서 확인해 주세요.")
	}
	rend := platform.VideoRendition{Width: 1280, Height: 720, Bandwidth: 3_000_000, Codecs: "avc1.640028,mp4a.40.2", FrameRate: 30}
	if req.MaxHeight > 0 && req.MaxHeight < 720 {
		rend.Width, rend.Height = 640, 360
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return platform.VideoFile{}, err
	}
	shift := []platform.TrackShift{{TrackID: 1, Timescale: 90000, Ticks: 180000}, {TrackID: 2, Timescale: 48000, Ticks: 96000}}
	st := platform.VideoResume{Height: rend.Height, TotalSegments: fakeVideoSegments, Shift: shift}
	flags := os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	if r := req.Resume; r != nil && r.Height == st.Height && r.TotalSegments == st.TotalSegments {
		st, flags = *r, os.O_WRONLY
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return platform.VideoFile{}, err
	}
	defer file.Close()
	if flags&os.O_TRUNC == 0 {
		if err := file.Truncate(st.Bytes); err != nil {
			return platform.VideoFile{}, err
		}
		if _, err := file.Seek(st.Bytes, 0); err != nil {
			return platform.VideoFile{}, err
		}
	} else {
		head := []byte("e2efake video init\n")
		if _, err := file.Write(head); err != nil {
			return platform.VideoFile{}, err
		}
		st.Bytes = int64(len(head))
	}
	per := int64(vodMs / fakeVideoSegments)
	for i := st.Segments; i < fakeVideoSegments; i++ {
		select {
		case <-ctx.Done():
			return platform.VideoFile{}, ctx.Err()
		case <-time.After(60 * time.Millisecond):
		}
		b := bytes.Repeat([]byte{byte('a' + i%26)}, 4096)
		if _, err := file.Write(b); err != nil {
			return platform.VideoFile{}, fmt.Errorf("write fake video: %w", err)
		}
		st.Bytes += int64(len(b))
		st.Segments, st.DoneMs, st.Fragments = i+1, int64(i+1)*per, st.Fragments+1
		if progress != nil {
			progress(platform.VideoProgress{Rendition: rend, State: st})
		}
	}
	return platform.VideoFile{Path: path, DurationMs: vodMs, Container: "fmp4", Rendition: rend, FirstPTSMs: 2000, State: st}, nil
}

var _ platform.VideoSource = (*Fake)(nil)
