package chzzk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

const liveMaster = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080
1080p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360
360p/index.m3u8
`

// liveServer serves a live stream whose playlist moves one 2-second
// segment on per read: reads 0..last, the last one ending the broadcast.
// Before the first segment it may give a program date-time, and the
// first fail reads of the playlist answer 403.
type liveServer struct {
	mu    sync.Mutex
	reads int
	last  int
	pdt   string
	fail  int
	gets  map[string]int
}

func (s *liveServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets[r.URL.Path]++
	switch r.URL.Path {
	case "/live/master.m3u8":
		w.Write([]byte(liveMaster))
	case "/live/360p/index.m3u8":
		if s.fail > 0 {
			s.fail--
			w.WriteHeader(http.StatusForbidden)
			return
		}
		k := s.reads
		s.reads++
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-MAP:URI=\"init.mp4\"\n", 10+k)
		if s.pdt != "" {
			fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n", s.pdt)
		}
		for n := 10 + k; n < 13+k; n++ {
			fmt.Fprintf(&b, "#EXTINF:2.000,\n%d.m4s?token=x\n", n)
		}
		if k >= s.last {
			b.WriteString("#EXT-X-ENDLIST\n")
		}
		w.Write([]byte(b.String()))
	case "/live/360p/init.mp4":
		w.Write(testInit())
	default:
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/live/360p/%d.m4s", &n); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(testSegment(n - 10))
	}
}

// clock is a fake time that sleeping moves on.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) sleep(ctx context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	return ctx.Err()
}

func record(t *testing.T, s *liveServer, resolve func(context.Context) (string, error)) ([]platform.LiveAudioSegment, *clock, error) {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	if resolve == nil {
		resolve = func(context.Context) (string, error) { return srv.URL + "/live/master.m3u8", nil }
	}
	c := &clock{t: time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC)}
	var segs []platform.LiveAudioSegment
	err := recordHLS(context.Background(), testClient(), resolve, filepath.Join(t.TempDir(), "rec"), allowAll,
		func(s platform.LiveAudioSegment) { segs = append(segs, s) }, c.now, c.sleep)
	return segs, c, err
}

// Each new segment is written once, in order, timed back from when it
// appeared; the recording ends with the playlist, and the segments join
// into one MP4 that starts at 0.
func TestRecordHLSFollowsALivePlaylist(t *testing.T) {
	s := &liveServer{last: 3, gets: map[string]int{}}
	segs, c, err := record(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 6 {
		t.Fatalf("segments = %d", len(segs))
	}
	start := time.Date(2026, 10, 22, 20, 0, 0, 0, time.UTC).Add(-6 * time.Second)
	for i, sg := range segs {
		if want := start.Add(time.Duration(i) * 2 * time.Second); !sg.At.Equal(want) || sg.DurMs != 2000 {
			t.Errorf("segment %d at %v (%d ms), want %v", i, sg.At, sg.DurMs, want)
		}
		if filepath.Base(sg.Path) != fmt.Sprintf("%012d.m4s", 10+i) || filepath.Base(sg.Init) != "init.mp4" {
			t.Errorf("segment %d files %s %s", i, sg.Path, sg.Init)
		}
		if info, err := os.Stat(sg.Path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("segment %d: %v", i, err)
		}
	}
	if s.gets["/live/360p/13.m4s"] != 1 || s.gets["/live/360p/init.mp4"] != 1 || s.gets["/live/master.m3u8"] != 1 {
		t.Errorf("requests %v", s.gets)
	}
	if c.t.Sub(start.Add(6*time.Second)) != 6*time.Second {
		t.Errorf("waited %v between reads, want the target duration", c.t.Sub(start.Add(6*time.Second)))
	}

	af, err := assembleAudio(segs, filepath.Join(t.TempDir(), "range"))
	if err != nil || filepath.Ext(af.Path) != ".mp4" || af.DurationMs != 12000 {
		t.Fatalf("assembled %+v, %v", af, err)
	}
	data, _ := os.ReadFile(af.Path)
	body := data[len(testInit()):]
	frags, err := fragments(body)
	if err != nil || len(frags) != 12 {
		t.Fatalf("fragments = %d, %v", len(frags), err)
	}
	if v, a := frags[0].get(body), frags[1].get(body); v != 4500 || a != 0 {
		t.Errorf("first fragment times = %d, %d", v, a)
	}
	if got := mfhdSeqs(t, body); len(got) != 12 || got[11] != 12 {
		t.Errorf("fragment numbers = %v", got)
	}
}

// A program date-time in the playlist times the segments.
func TestRecordHLSUsesProgramDateTime(t *testing.T) {
	s := &liveServer{last: 0, pdt: "2026-10-22T19:30:00.000Z", gets: map[string]int{}}
	segs, _, err := record(t, s, nil)
	if err != nil || len(segs) != 3 {
		t.Fatalf("segments = %d, %v", len(segs), err)
	}
	if want := time.Date(2026, 10, 22, 19, 30, 4, 0, time.UTC); !segs[2].At.Equal(want) {
		t.Errorf("third segment at %v, want %v", segs[2].At, want)
	}
}

// A playlist that stops answering is fetched again from a fresh URL; an
// ended broadcast ends the recording; a restricted one fails it.
func TestRecordHLSRecoversAndStops(t *testing.T) {
	s := &liveServer{last: 1, fail: 2, gets: map[string]int{}}
	segs, _, err := record(t, s, nil)
	if err != nil || len(segs) != 4 || s.gets["/live/master.m3u8"] != 3 {
		t.Fatalf("segments = %d, master reads = %d, %v", len(segs), s.gets["/live/master.m3u8"], err)
	}

	offline := func(context.Context) (string, error) {
		return "", domain.NewUserError("LIVE_OFFLINE", "지금은 방송 중이 아닙니다.")
	}
	if segs, _, err := record(t, &liveServer{gets: map[string]int{}}, offline); err != nil || len(segs) != 0 {
		t.Errorf("offline: %d segments, %v", len(segs), err)
	}
	restricted := func(context.Context) (string, error) {
		return "", domain.NewUserError("PLAYBACK_RESTRICTED", "연령 제한")
	}
	if _, _, err := record(t, &liveServer{gets: map[string]int{}}, restricted); !isCode(err, "PLAYBACK_RESTRICTED") {
		t.Errorf("restricted: %v", err)
	}
}

// Media off the allowed hosts is refused before it is fetched.
func TestRecordHLSRefusesOtherHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:2,\nhttps://evil.example/a.ts\n"))
	}))
	defer srv.Close()
	host := srv.Listener.Addr().String()
	allow := func(u *url.URL) bool { return u.Host == host }
	c := &clock{}
	err := recordHLS(context.Background(), testClient(), func(context.Context) (string, error) { return srv.URL + "/m.m3u8", nil },
		t.TempDir(), allow, nil, c.now, c.sleep)
	if !errors.Is(err, platform.ErrSchemaChanged) {
		t.Fatalf("err = %v", err)
	}
}

func TestAllowedLiveMedia(t *testing.T) {
	cases := map[string]bool{
		"https://a.navercdn.com/x.m3u8":    true,
		"https://live.pstatic.net/x.m3u8":  true,
		"http://live.pstatic.net/x.m3u8":   false,
		"https://pstatic.net.evil.com/x":   false,
		"https://live.pstatic.net:8443/x":  false,
		"https://u@live.pstatic.net/x.m4s": false,
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		if got := allowedLiveMedia(u); got != want {
			t.Errorf("%s = %v, want %v", raw, got, want)
		}
	}
}

func isCode(err error, code string) bool {
	ue, ok := domain.AsUserError(err)
	return ok && ue.Code == code
}
