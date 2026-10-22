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

// liveAudioSeg is a recorded segment's length on the synthetic clock.
const liveAudioSeg = 10 * time.Second

// RecordLiveAudio "records" the synthetic broadcast: a segment of silent
// AAC for every 10 synthetic seconds from the first connection, as the
// chat clock moves (faster than real time, like the chat). Channels
// "offline" and "novideo" have no stream.
func (f *Fake) RecordLiveAudio(ctx context.Context, t platform.Target, dir string, onSegment func(platform.LiveAudioSegment)) error {
	if t.ID == "offline" || t.ID == "novideo" {
		return domain.NewUserError("PLAYBACK_UNAVAILABLE", "방송 영상 주소를 확인하지 못했습니다. 치지직에서 시청해 주세요.")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var next time.Time
	for n := int64(0); ; {
		f.mu.Lock()
		first, now := f.first, f.clock
		f.mu.Unlock()
		if next.IsZero() {
			next = first
		}
		for !next.IsZero() && !next.Add(liveAudioSeg).After(now) {
			p := filepath.Join(dir, fmt.Sprintf("%06d.aac", n))
			if err := os.WriteFile(p, segment(n), 0o600); err != nil {
				return err
			}
			onSegment(platform.LiveAudioSegment{Path: p, At: next, DurMs: liveAudioSeg.Milliseconds()})
			next, n = next.Add(liveAudioSeg), n+1
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// AssembleAudio joins the segments (packed AAC joins as it is).
func (f *Fake) AssembleAudio(segs []platform.LiveAudioSegment, dst string) (platform.AudioFile, error) {
	out := dst + ".aac"
	file, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return platform.AudioFile{}, err
	}
	defer file.Close()
	var total int64
	for _, s := range segs {
		b, err := os.ReadFile(s.Path)
		if err != nil {
			return platform.AudioFile{}, err
		}
		if _, err := file.Write(b); err != nil {
			return platform.AudioFile{}, err
		}
		total += s.DurMs
	}
	return platform.AudioFile{Path: out, DurationMs: total}, nil
}
