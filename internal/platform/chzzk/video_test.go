package chzzk

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// Synthetic fragmented MP4: a video track (90 kHz) and an audio track
// (48 kHz) whose first fragments start at 2.1 s and 2.05 s, as CHZZK's
// segments start a little after 0.

func mp4box(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	out := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(out, uint32(8+len(body)))
	copy(out[4:], typ)
	return append(out, body...)
}

func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func u64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

func testTrak(id, timescale uint32) []byte {
	tkhd := mp4box("tkhd", u32(0x7), u32(0), u32(0), u32(id), make([]byte, 64))
	mdhd := mp4box("mdhd", u32(0), u32(0), u32(0), u32(timescale), u32(0), make([]byte, 4))
	return mp4box("trak", tkhd, mp4box("mdia", mdhd))
}

func testInit() []byte {
	return append(mp4box("ftyp", []byte("iso6"), u32(0)), mp4box("moov", mp4box("mvhd", make([]byte, 100)), testTrak(1, 90000), testTrak(2, 48000))...)
}

func testTraf(track uint32, base uint64) []byte {
	return mp4box("traf", mp4box("tfhd", u32(0x020000), u32(track)), mp4box("tfdt", u32(1<<24), u64(base)), mp4box("trun", u32(0), u32(0)))
}

func testSegment(n int) []byte {
	v := uint64(189000 + n*180000) // 2.1 s + 2 s per segment
	a := uint64(98400 + n*96000)   // 2.05 s + 2 s per segment
	return bytes.Join([][]byte{
		mp4box("styp", []byte("msdh"), u32(0)),
		mp4box("moof", mp4box("mfhd", u32(0), u32(uint32(n+1))), testTraf(1, v)), mp4box("mdat", []byte("video")),
		mp4box("emsg", []byte("id3")),
		mp4box("moof", mp4box("mfhd", u32(0), u32(uint32(n+1))), testTraf(2, a)), mp4box("mdat", []byte("audio")),
	}, nil)
}

func decodeTimes(t *testing.T, seg []byte) map[uint32]uint64 {
	t.Helper()
	frags, err := fragments(seg)
	if err != nil {
		t.Fatal(err)
	}
	out := map[uint32]uint64{}
	for _, f := range frags {
		out[f.track] = f.get(seg)
	}
	return out
}

func TestRebaseMovesEveryTrackBackByTheEarliestStart(t *testing.T) {
	ts, err := initTimescales(testInit())
	if err != nil || ts[1] != 90000 || ts[2] != 48000 {
		t.Fatalf("timescales = %v, %v", ts, err)
	}
	shift, firstMs, err := chooseShift(testSegment(0), ts)
	if err != nil {
		t.Fatal(err)
	}
	// The audio starts first (2.05 s); the video moves back by the same time in its own timescale.
	want := []platform.TrackShift{{TrackID: 1, Timescale: 90000, Ticks: 184500}, {TrackID: 2, Timescale: 48000, Ticks: 98400}}
	if firstMs != 2050 || len(shift) != 2 || shift[0] != want[0] || shift[1] != want[1] {
		t.Fatalf("shift = %v, first %d ms", shift, firstMs)
	}
	for n, wantV := range []uint64{4500, 184500} {
		out, err := rebaseSegment(testSegment(n), shift)
		if err != nil {
			t.Fatal(err)
		}
		times := decodeTimes(t, out)
		if times[1] != wantV || times[2] != uint64(n*96000) {
			t.Errorf("segment %d times = %v", n, times)
		}
		top, _ := boxes(out, 0, len(out))
		var types []string
		for _, b := range top {
			types = append(types, b.typ)
		}
		if got := strings.Join(types, ","); got != "moof,mdat,moof,mdat" {
			t.Errorf("segment %d boxes = %s", n, got)
		}
	}
	// A segment that starts before the first one is refused, not wrapped around.
	if _, err := rebaseSegment(testSegment(0), []platform.TrackShift{{TrackID: 1, Timescale: 90000, Ticks: 1 << 40}, {TrackID: 2, Timescale: 48000}}); err == nil {
		t.Error("underflow accepted")
	}
	// An absolute base data offset cannot survive joining segments.
	bad := mp4box("moof", mp4box("traf", mp4box("tfhd", u32(0x1), u32(1), u64(0)), mp4box("tfdt", u32(0), u32(0))))
	if _, err := fragments(bad); err == nil {
		t.Error("absolute base data offset accepted")
	}
}

const videoMaster = `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-STREAM-INF:BANDWIDTH=3192000,CODECS="avc1.640028,mp4a.40.2",RESOLUTION=1280x720,FRAME-RATE=60.00
720p/chunklist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=696000,CODECS="avc1.4D001E,mp4a.40.2",RESOLUTION=640x360,FRAME-RATE=30.00
360p/chunklist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=8384000,CODECS="avc1.64002A,mp4a.40.2",RESOLUTION=1920x1080,FRAME-RATE=60.00
1080p/chunklist.m3u8
`

func TestPickVideo(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/vod/master.m3u8")
	for _, c := range []struct {
		max  int
		want string
	}{{720, "720p"}, {1080, "1080p"}, {0, "1080p"}, {480, "360p"}, {144, "360p"}} {
		v, err := pickVideo(videoMaster, base, c.max)
		if err != nil || !strings.Contains(v.url.Path, "/"+c.want+"/") {
			t.Errorf("max %d: %v %v", c.max, v.url, err)
		}
	}
	v, _ := pickVideo(videoMaster, base, 720)
	if v.rendition.Width != 1280 || v.rendition.FrameRate != 60 || v.rendition.Codecs != "avc1.640028,mp4a.40.2" {
		t.Errorf("rendition = %+v", v.rendition)
	}
	if _, err := pickVideo("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\na.m3u8\n", base, 720); !errors.Is(err, platform.ErrSchemaChanged) {
		t.Errorf("no resolution: %v", err)
	}
}

// videoServer serves the master, a 720p playlist of three segments and the
// media; missing names segments answered 404.
func videoServer(t *testing.T, media string, missing map[string]bool) (*httptest.Server, *int) {
	t.Helper()
	gets := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gets++
		name := filepath.Base(r.URL.Path)
		switch {
		case r.URL.Path == "/vod/master.m3u8":
			w.Write([]byte(videoMaster))
		case strings.HasSuffix(r.URL.Path, "chunklist.m3u8"):
			w.Write([]byte(media))
		case missing[name]:
			http.NotFound(w, r)
		case name == "init.m4s":
			w.Write(testInit())
		case strings.HasPrefix(name, "seg"):
			n := int(name[3] - '0')
			w.Write(testSegment(n))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, gets
}

const videoMedia = `#EXTM3U
#EXT-X-TARGETDURATION:2
#EXT-X-MAP:URI="init.m4s"
#EXTINF:2.000,
seg0.m4v
#EXTINF:2.000,
seg1.m4v
#EXTINF:2.000,
seg2.m4v
#EXT-X-ENDLIST
`

func TestDownloadVideoJoinsRebasedSegments(t *testing.T) {
	srv, _ := videoServer(t, videoMedia, nil)
	path := filepath.Join(t.TempDir(), "bundle", "video.mp4.part")
	var last platform.VideoProgress
	vf, err := downloadVideo(context.Background(), testClient(), srv.URL+"/vod/master.m3u8", path, platform.VideoRequest{MaxHeight: 720},
		allowAll, func(p platform.VideoProgress) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if vf.Rendition.Height != 720 || vf.DurationMs != 6000 || vf.FirstPTSMs != 2050 || vf.State.Segments != 3 || len(vf.State.Missing) != 0 {
		t.Fatalf("file = %+v", vf)
	}
	data, _ := os.ReadFile(path)
	if int64(len(data)) != vf.State.Bytes || last.State.Bytes != vf.State.Bytes {
		t.Fatalf("size %d, state %d, progress %d", len(data), vf.State.Bytes, last.State.Bytes)
	}
	if !bytes.HasPrefix(data, testInit()) {
		t.Fatal("file does not start with the init section")
	}
	rest := data[len(testInit()):]
	frags, err := fragments(rest)
	if err != nil || len(frags) != 6 {
		t.Fatalf("fragments = %d, %v", len(frags), err)
	}
	if v, a := frags[0].get(rest), frags[1].get(rest); v != 4500 || a != 0 {
		t.Errorf("first fragment times = %d, %d", v, a)
	}
	if bytes.Contains(data, []byte("styp")) || bytes.Contains(data, []byte("emsg")) {
		t.Error("boxes without media were kept")
	}
}

func TestDownloadVideoResumesAndRecordsMissingSegments(t *testing.T) {
	srv, _ := videoServer(t, videoMedia, map[string]bool{"seg1.m4v": true})
	path := filepath.Join(t.TempDir(), "video.mp4.part")
	ctx, cancel := context.WithCancel(context.Background())
	var state platform.VideoResume
	_, err := downloadVideo(ctx, testClient(), srv.URL+"/vod/master.m3u8", path, platform.VideoRequest{MaxHeight: 720}, allowAll,
		func(p platform.VideoProgress) {
			state = p.State
			cancel() // stop after the first segment
		})
	if !errors.Is(err, context.Canceled) || state.Segments != 1 {
		t.Fatalf("first run: %v, state %+v", err, state)
	}
	// Bytes written after the recorded state are cut off on resume.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.Write([]byte("torn write"))
	f.Close()
	vf, err := downloadVideo(context.Background(), testClient(), srv.URL+"/vod/master.m3u8", path,
		platform.VideoRequest{MaxHeight: 720, Resume: &state}, allowAll, nil)
	if err != nil {
		t.Fatal(err)
	}
	if vf.State.Segments != 3 || len(vf.State.Missing) != 1 || vf.State.Missing[0] != (domain.Gap{StartMs: 2000, EndMs: 4000}) {
		t.Fatalf("state = %+v", vf.State)
	}
	data, _ := os.ReadFile(path)
	if int64(len(data)) != vf.State.Bytes || bytes.Contains(data, []byte("torn write")) {
		t.Fatalf("size %d, state %d", len(data), vf.State.Bytes)
	}
	frags, _ := fragments(data[len(testInit()):])
	if len(frags) != 4 {
		t.Fatalf("fragments = %d", len(frags))
	}
	// A different rendition starts over instead of appending to the wrong file.
	other := state
	other.Height = 1080
	vf, err = downloadVideo(context.Background(), testClient(), srv.URL+"/vod/master.m3u8", path,
		platform.VideoRequest{MaxHeight: 720, Resume: &other}, allowAll, nil)
	if err != nil || vf.State.Segments != 3 {
		t.Fatalf("restart: %+v, %v", vf.State, err)
	}
}

func TestDownloadVideoRefusesWhatCannotBecomeMP4(t *testing.T) {
	ts := strings.Replace(strings.Replace(videoMedia, "#EXT-X-MAP:URI=\"init.m4s\"\n", "", 1), ".m4v", ".ts", -1)
	srv, _ := videoServer(t, ts, nil)
	_, err := downloadVideo(context.Background(), testClient(), srv.URL+"/vod/master.m3u8", filepath.Join(t.TempDir(), "v"),
		platform.VideoRequest{}, allowAll, nil)
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "MEDIA" {
		t.Fatalf("TS: %v", err)
	}
	srv, _ = videoServer(t, strings.Replace(videoMedia, "#EXT-X-ENDLIST\n", "", 1), nil)
	_, err = downloadVideo(context.Background(), testClient(), srv.URL+"/vod/master.m3u8", filepath.Join(t.TempDir(), "v"),
		platform.VideoRequest{}, allowAll, nil)
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "MEDIA" {
		t.Fatalf("open playlist: %v", err)
	}
	// URLs off the CDN are refused before any request.
	srv, gets := videoServer(t, videoMedia, nil)
	_, err = downloadVideo(context.Background(), testClient(), srv.URL+"/vod/master.m3u8", filepath.Join(t.TempDir(), "v"),
		platform.VideoRequest{}, func(*url.URL) bool { return false }, nil)
	if err == nil || *gets != 0 {
		t.Fatalf("disallowed: %v after %d requests", err, *gets)
	}
}

func TestAudioDownloadRebasesFragmentedMP4(t *testing.T) {
	srv, _ := videoServer(t, videoMedia, nil)
	// The master has no audio-only rendition, so the lightest variant (360p) is taken, as on CHZZK.
	af, err := downloadHLS(context.Background(), testClient(), srv.URL+"/vod/master.m3u8", filepath.Join(t.TempDir(), "a"), allowAll, nil)
	if err != nil || filepath.Ext(af.Path) != ".mp4" {
		t.Fatalf("audio = %+v, %v", af, err)
	}
	data, _ := os.ReadFile(af.Path)
	frags, err := fragments(data[len(testInit()):])
	if err != nil || len(frags) != 6 {
		t.Fatalf("fragments = %d, %v", len(frags), err)
	}
	if v, a := frags[0].get(data[len(testInit()):]), frags[1].get(data[len(testInit()):]); v != 4500 || a != 0 {
		t.Errorf("first fragment times = %d, %d", v, a)
	}
}
