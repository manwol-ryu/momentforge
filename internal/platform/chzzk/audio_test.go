package chzzk

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

const masterPlaylist = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,AUDIO="aud"
1080p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,AUDIO="aud"
360p/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1500000,RESOLUTION=1280x720
720p/index.m3u8
`

const audioPlaylist = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-TARGETDURATION:4
#EXT-X-MAP:URI="init.mp4"
#EXTINF:4.000,
seg0.m4s
#EXTINF:4.000,
seg1.m4s
#EXTINF:2.500,
seg2.m4s
#EXT-X-ENDLIST
`

func allowAll(*url.URL) bool { return true }

func testClient() *platform.HTTPClient {
	c := newCDNClient()
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestPickRendition(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/vod/master.m3u8")
	cases := []struct {
		name, body, want string
		master           bool
	}{
		{"lowest variant", masterPlaylist, "https://cdn.example/vod/360p/index.m3u8", true},
		{"audio only wins", "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"ko\",URI=\"audio/ko.m3u8\"\n" + masterPlaylist[8:],
			"https://cdn.example/vod/audio/ko.m3u8", true},
		{"default audio wins", "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"a\",URI=\"a.m3u8\"\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"b\",DEFAULT=YES,URI=\"b.m3u8\"\n",
			"https://cdn.example/vod/b.m3u8", true},
		{"subtitles ignored", "#EXTM3U\n#EXT-X-MEDIA:TYPE=SUBTITLES,URI=\"sub.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=10\nlow.m3u8\n", "https://cdn.example/vod/low.m3u8", true},
		{"already media", audioPlaylist, "https://cdn.example/vod/master.m3u8", false},
	}
	for _, c := range cases {
		u, master, err := pickRendition(c.body, base)
		if err != nil || u.String() != c.want || master != c.master {
			t.Errorf("%s: %v %v %v", c.name, u, master, err)
		}
	}
	if _, _, err := pickRendition("#EXTM3U\n#EXT-X-VERSION:3\n", base); !errors.Is(err, platform.ErrSchemaChanged) {
		t.Errorf("empty master: %v", err)
	}
}

func TestParseMediaPlaylist(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/vod/audio/ko.m3u8")
	pl, err := parseMediaPlaylist(audioPlaylist, base)
	if err != nil || pl.init == nil || pl.init.String() != "https://cdn.example/vod/audio/init.mp4" || len(pl.segments) != 3 || !pl.ended {
		t.Fatalf("playlist = %+v, %v", pl, err)
	}
	if pl.segments[2].url.String() != "https://cdn.example/vod/audio/seg2.m4s" || pl.segments[2].durMs != 2500 {
		t.Errorf("segment 2 = %+v", pl.segments[2])
	}
	if containerExt(pl) != ".mp4" {
		t.Errorf("ext = %s", containerExt(pl))
	}
	ts, _ := parseMediaPlaylist("#EXTM3U\n#EXTINF:6,\na.ts\n#EXT-X-ENDLIST\n", base)
	if containerExt(ts) != ".ts" {
		t.Errorf("ts ext = %s", containerExt(ts))
	}
	_, err = parseMediaPlaylist("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n#EXTINF:6,\na.ts\n", base)
	var ue *domain.UserError
	if !errors.As(err, &ue) || ue.Code != "PLAYBACK_RESTRICTED" {
		t.Errorf("encrypted: %v", err)
	}
}

func TestParseAttrs(t *testing.T) {
	got := parseAttrs(`TYPE=AUDIO,GROUP-ID="aud",NAME="한국어, 기본",DEFAULT=YES,URI="a/b.m3u8"`)
	want := map[string]string{"TYPE": "AUDIO", "GROUP-ID": "aud", "NAME": "한국어, 기본", "DEFAULT": "YES", "URI": "a/b.m3u8"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// A master playlist, its lowest rendition and three fMP4 parts come down
// into one file, in order, with progress per segment.
func TestDownloadHLSWritesOneFile(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/vod/master.m3u8":
			w.Write([]byte(masterPlaylist))
		case "/vod/360p/index.m3u8":
			w.Write([]byte(audioPlaylist))
		case "/vod/360p/init.mp4":
			w.Write([]byte("INIT|"))
		case "/vod/360p/seg0.m4s", "/vod/360p/seg1.m4s", "/vod/360p/seg2.m4s":
			w.Write([]byte(strings.TrimSuffix(filepath.Base(r.URL.Path), ".m4s") + "|"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "audio", "s1")
	var progress []platform.AudioProgress
	af, err := downloadHLS(context.Background(), testClient(), srv.URL+"/vod/master.m3u8", dst, allowAll, func(p platform.AudioProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if af.Path != dst+".mp4" || af.DurationMs != 10500 {
		t.Errorf("file = %+v", af)
	}
	data, err := os.ReadFile(af.Path)
	if err != nil || string(data) != "INIT|seg0|seg1|seg2|" {
		t.Errorf("content = %q, %v", data, err)
	}
	if info, _ := os.Stat(af.Path); info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v", info.Mode().Perm())
	}
	if len(progress) != 3 || progress[2].Segments != 3 || progress[2].TotalSegments != 3 || progress[2].DoneMs != 10500 || progress[2].Bytes != 20 {
		t.Errorf("progress = %+v", progress)
	}
	if requests.Load() != 6 {
		t.Errorf("requests = %d", requests.Load())
	}
}

// Segment URLs off the allowed hosts stop the download before any media request.
func TestDownloadHLSRefusesOtherHosts(t *testing.T) {
	var media atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/m.m3u8":
			w.Write([]byte("#EXTM3U\n#EXTINF:4,\nhttps://evil.example/a.ts\n#EXT-X-ENDLIST\n"))
		default:
			media.Add(1)
		}
	}))
	defer srv.Close()
	host := srv.Listener.Addr().String()
	allow := func(u *url.URL) bool { return u.Host == host }
	dst := filepath.Join(t.TempDir(), "s2")
	_, err := downloadHLS(context.Background(), testClient(), srv.URL+"/m.m3u8", dst, allow, nil)
	if !errors.Is(err, platform.ErrSchemaChanged) {
		t.Fatalf("err = %v", err)
	}
	if media.Load() != 0 {
		t.Error("a media request was made")
	}
	if _, err := os.Stat(dst + ".ts"); !os.IsNotExist(err) {
		t.Error("partial file left behind")
	}
	if _, err := downloadHLS(context.Background(), testClient(), "https://evil.example/m.m3u8", dst, allow, nil); err == nil {
		t.Error("a playlist off the allowed hosts was fetched")
	}
}

func TestAllowedMedia(t *testing.T) {
	cases := map[string]bool{
		"https://a.navercdn.com/x.m3u8":       true,
		"https://a.navercdn.com:443/x.m3u8":   true,
		"http://a.navercdn.com/x.m3u8":        false,
		"https://user@a.navercdn.com/x.m3u8":  false,
		"https://navercdn.com.evil.com/x":     false,
		"https://a.navercdn.com:8443/x.m3u8":  false,
		"https://live.pstatic.net/x.m3u8":     false,
		"https://a.navercdn.com/../../x.m3u8": true,
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		if got := allowedMedia(u); got != want {
			t.Errorf("%s = %v, want %v", raw, got, want)
		}
	}
}
