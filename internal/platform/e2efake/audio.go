//go:build e2efake

package e2efake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// VODAudio writes the synthetic VOD's "audio": the silent packed-AAC
// segments media.go serves, 20 minutes' worth, with progress per segment,
// so the download step can be exercised without any network. Video
// "noaudio" has no stream to download.
func (f *Fake) VODAudio(ctx context.Context, t platform.Target, dst string, progress func(platform.AudioProgress)) (platform.AudioFile, error) {
	if t.ID == "noaudio" {
		return platform.AudioFile{}, domain.NewUserError("PLAYBACK_UNAVAILABLE", "공개 재생 주소를 확인하지 못했습니다. 이 영상은 치지직에서 확인해 주세요.")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return platform.AudioFile{}, err
	}
	out := dst + ".aac"
	file, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return platform.AudioFile{}, err
	}
	defer file.Close()
	const n = 40 // 40 segments of 30 synthetic seconds: a visible but short download
	var written int64
	for i := range n {
		select {
		case <-ctx.Done():
			return platform.AudioFile{}, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
		b := segment(int64(i))
		if _, err := file.Write(b); err != nil {
			return platform.AudioFile{}, fmt.Errorf("write fake audio: %w", err)
		}
		written += int64(len(b))
		if progress != nil {
			progress(platform.AudioProgress{Segments: i + 1, TotalSegments: n, Bytes: written, DoneMs: int64(i+1) * vodMs / n})
		}
	}
	return platform.AudioFile{Path: out, DurationMs: vodMs}, nil
}
