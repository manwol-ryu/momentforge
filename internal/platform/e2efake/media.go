//go:build e2efake

package e2efake

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// The synthetic broadcast's stream is a live HLS playlist of silent AAC
// (packed audio: an ID3 timestamp, then ADTS frames), generated on request by
// a server on a random loopback port. It needs no media files, and the
// browser decodes it, so E2E can press play and watch the time advance.
const (
	segFrames = 75                      // AAC frames per segment: 75 × 1024 samples = 1.6 s at 48 kHz
	segDur    = 1600 * time.Millisecond // segment length
	segPTS    = 144_000                 // segment length on the 90 kHz clock
	window    = 5                       // segments in the playlist
)

// silentFrame is one mono AAC-LC frame of silence (hls.js uses the same bytes to fill gaps).
var silentFrame = []byte{0x00, 0xc8, 0x00, 0x80, 0x23, 0x80}

// LivePlayback returns the synthetic stream. Channel "novideo" broadcasts
// chat without a playable stream; "offline" is not broadcasting.
func (f *Fake) LivePlayback(ctx context.Context, t platform.Target) (platform.Playback, error) {
	switch t.ID {
	case "offline":
		return platform.Playback{}, domain.NewUserError("LIVE_OFFLINE", "지금은 방송 중이 아니라 영상을 불러올 수 없습니다.")
	case "novideo":
		return platform.Playback{}, domain.NewUserError("PLAYBACK_UNAVAILABLE", "방송 영상 주소를 확인하지 못했습니다. 치지직에서 시청해 주세요.")
	}
	origin, err := f.mediaOrigin()
	if err != nil {
		return platform.Playback{}, err
	}
	return platform.Playback{URL: origin + "/live/" + url.PathEscape(t.ID) + "/index.m3u8", Type: "hls"}, nil
}

// MediaOrigins is the loopback stream server, started on first use. It
// runs as long as the E2E binary does.
func (f *Fake) MediaOrigins() []string {
	origin, err := f.mediaOrigin()
	if err != nil {
		return nil
	}
	return []string{origin}
}

func (f *Fake) mediaOrigin() (string, error) {
	f.mediaOnce.Do(func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			f.mediaErr = fmt.Errorf("e2efake media server: %w", err)
			return
		}
		start := time.Now()
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serveMedia(w, r, start)
		}), ReadHeaderTimeout: 5 * time.Second}
		go srv.Serve(l)
		f.media = "http://" + l.Addr().String()
	})
	return f.media, f.mediaErr
}

// serveMedia answers /live/{channel}/index.m3u8 and /live/{channel}/{n}.aac.
func serveMedia(w http.ResponseWriter, r *http.Request, start time.Time) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/live/"), "/")
	if len(parts) != 2 || parts[0] == "" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	// 1000 segments in, so the first playlist already has a full window.
	last := 1000 + int64(time.Since(start)/segDur)
	if parts[1] == "index.m3u8" {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:%d\n", last-window+1)
		for n := last - window + 1; n <= last; n++ {
			fmt.Fprintf(&b, "#EXTINF:1.600,\n%d.aac\n", n)
		}
		w.Write([]byte(b.String()))
		return
	}
	n, err := strconv.ParseInt(strings.TrimSuffix(parts[1], ".aac"), 10, 64)
	if err != nil || !strings.HasSuffix(parts[1], ".aac") || n < 0 || n > last {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/aac")
	w.Write(segment(n))
}

// segment is segment n: an ID3 tag carrying its start time, then silence.
func segment(n int64) []byte {
	const owner = "com.apple.streaming.transportStreamTimestamp"
	priv := append([]byte(owner+"\x00"), make([]byte, 8)...)
	binary.BigEndian.PutUint64(priv[len(owner)+1:], uint64(n*segPTS)&(1<<33-1))
	frame := append([]byte("PRIV"), byte(0), byte(0), byte(0), byte(len(priv)), 0, 0) // sizes < 128 are the same syncsafe or not
	frame = append(frame, priv...)
	out := append([]byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, byte(len(frame))}, frame...)

	// ADTS header: MPEG-4 AAC LC, 48 kHz (index 3), mono, no CRC.
	size := 7 + len(silentFrame)
	adts := []byte{0xff, 0xf1, 0x4c, 0x40 | byte(size>>11), byte(size >> 3), byte(size&7)<<5 | 0x1f, 0xfc}
	for range segFrames {
		out = append(append(out, adts...), silentFrame...)
	}
	return out
}
