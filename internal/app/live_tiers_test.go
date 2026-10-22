package app

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// liveWithAudio is a LIVE platform that also gives its audio: on record it
// writes 6-second segments from minute 9 of the broadcast up to now at
// once, then waits for the capture to end.
type liveWithAudio struct {
	*fakeLive
	origin time.Time
}

func (f *liveWithAudio) RecordLiveAudio(ctx context.Context, t platform.Target, dir string, onSegment func(platform.LiveAudioSegment)) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for at := f.origin.Add(9 * time.Minute); at.Before(time.Now()); at = at.Add(6 * time.Second) {
		p := filepath.Join(dir, fmt.Sprintf("%d.aac", at.UnixMilli()))
		if err := os.WriteFile(p, []byte("seg|"), 0o600); err != nil {
			return err
		}
		onSegment(platform.LiveAudioSegment{Path: p, At: at, DurMs: 6000})
	}
	<-ctx.Done()
	return nil
}

func (f *liveWithAudio) AssembleAudio(segs []platform.LiveAudioSegment, dst string) (platform.AudioFile, error) {
	var b strings.Builder
	var total int64
	for _, s := range segs {
		data, err := os.ReadFile(s.Path)
		if err != nil {
			return platform.AudioFile{}, err
		}
		b.Write(data)
		total += s.DurMs
	}
	out := dst + ".aac"
	return platform.AudioFile{Path: out, DurationMs: total}, os.WriteFile(out, []byte(b.String()), 0o600)
}

// rangeEngine hears a line every 20 seconds of whatever file it gets.
type rangeEngine struct {
	mu    sync.Mutex
	files int
}

func (e *rangeEngine) ID() string                                   { return "range" }
func (e *rangeEngine) Name() string                                 { return "구간 인식" }
func (e *rangeEngine) Ready(context.Context) (bool, string)         { return true, "" }
func (e *rangeEngine) Probe(context.Context, string) (int64, error) { return 0, nil }
func (e *rangeEngine) Transcribe(ctx context.Context, audio string, opts asr.Options, progress func(asr.Progress)) ([]domain.Utterance, error) {
	if _, err := os.Stat(audio); err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.files++
	e.mu.Unlock()
	var out []domain.Utterance
	for ms := int64(5000); ms < 400_000; ms += 20_000 {
		out = append(out, domain.Utterance{StartMs: ms, EndMs: ms + 3000, Speaker: "방송자", Text: "가짜 발언 " + fmt.Sprint(ms/1000)})
	}
	return out, nil
}

// A capture with tiers shows the laugh burst as an early candidate, judges
// it once five minutes of chat followed, transcribes its range from the
// recorded audio, has AI fix the lines and explain it, and deletes the
// audio when the capture ends.
func TestLiveJudgesTranscribesAndExplains(t *testing.T) {
	origin := time.Now().Add(-40 * time.Minute)
	src := &liveWithAudio{fakeLive: newFakeLive(origin), origin: origin}
	src.script = func(ctx context.Context, n int, sink platform.LiveSink) error {
		sink.Connected(origin.Add(10 * time.Minute))
		if _, err := feed(sink, origin, 10*minute+1000, 40*minute, 20*minute); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	a := newLiveApp(t, src, func(lt *liveTiming) { lt.autoEvery = 10 * time.Millisecond })
	engine := &rangeEngine{}
	a.cfg.ASR.Register(engine)
	p := &taskAI{}
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(p)
	a.initAI()
	if _, err := a.SetAIKey("taskai", "k", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveSettings(Settings{AIProvider: "taskai", AutoAI: true, AutoTranscriptFix: true, LiveAudio: true}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	info, err := a.StartLive(ctx, LiveRequest{URL: "live:ch1"})
	if err != nil {
		t.Fatal(err)
	}
	var judged domain.Candidate
	waitLive(t, a, info.ID, "the burst to be judged", func(v LiveView) bool {
		cands, _, _ := a.LiveCandidates(ctx, info.ID, 0)
		for _, c := range cands {
			if c.Tier == domain.TierJudged && c.State == domain.CandidateConfirmed {
				judged = c.Candidate
				return true
			}
		}
		return false
	})
	if judged.ID != "b0001" || judged.ReactionStartMs < 20*minute-10_000 || judged.ReactionStartMs > 20*minute+20_000 || judged.Strength == "" {
		t.Fatalf("judged = %+v", judged)
	}
	cands, _, _ := a.LiveCandidates(ctx, info.ID, 0)
	for _, c := range cands {
		if c.Tier == domain.TierEarly && c.ReactionStartMs < 21*minute && (c.State != domain.CandidateDropped || c.ReplacedBy != "b0001") {
			t.Fatalf("early %s: %s, replaced by %q", c.ID, c.State, c.ReplacedBy)
		}
	}

	// Its range was transcribed and fixed, and AI explained it with the speech.
	var tl *UnifiedTimeline
	waitLive(t, a, info.ID, "the explanation", func(v LiveView) bool {
		tl, _ = a.Timeline(ctx, info.ID)
		for _, it := range tl.Items {
			if it.Source == SourceAI && strings.Contains(strings.Join(it.SourceCandidateIDs, ","), "rule:b0001") {
				return true
			}
		}
		return false
	})
	tv, ok, err := a.Transcript(ctx, info.ID)
	if err != nil || !ok || tv.Utterances == 0 || tv.Fixed != tv.Utterances || len(tv.Coverage) != 1 {
		v, _ := a.LiveStatus(ctx, info.ID)
		t.Fatalf("transcript %+v %v %v (audio %q %q, auto %q)", tv, ok, err, v.Audio, v.AudioNote, v.AutoNote)
	}
	if c := tv.Coverage[0]; c.StartMs != max(0, judged.ClipStartMs-liveRangePadMs) || c.EndMs != judged.ClipEndMs+liveRangePadMs {
		t.Errorf("coverage %+v, clip %d-%d", c, judged.ClipStartMs, judged.ClipEndMs)
	}
	utts, _ := a.Utterances(ctx, info.ID, 0, math.MaxInt32, 100)
	for _, u := range utts {
		if u.StartMs < tv.Coverage[0].StartMs || u.StartMs >= tv.Coverage[0].EndMs || !strings.HasPrefix(u.Text, "가짜 발화") {
			t.Fatalf("utterance %+v", u)
		}
	}
	p.mu.Lock()
	steps, models := strings.Join(p.steps, ","), strings.Join(p.models, ",")
	p.mu.Unlock()
	if steps != "transcript_fix:fast" || !strings.HasPrefix(models, "balanced") {
		t.Errorf("steps %s, review models %s", steps, models)
	}
	if v, _ := a.LiveStatus(ctx, info.ID); !v.Tiered || v.Audio != audioRecording || v.AutoNote != "" {
		t.Errorf("status audio=%q note=%q auto=%q", v.Audio, v.AudioNote, v.AutoNote)
	}

	// Stopping settles it and deletes the recorded audio.
	a.StopLive(info.ID)
	waitLive(t, a, info.ID, "stop", stopped)
	deadline := time.Now().Add(5 * time.Second)
	for {
		left, _ := filepath.Glob(filepath.Join(a.runtimeAudioDir(), "live-*"))
		if len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("audio left behind: %v", left)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cands, _, _ = a.LiveCandidates(ctx, info.ID, 0)
	for _, c := range cands {
		if c.ID == "b0001" && c.Tier != domain.TierFinal {
			t.Errorf("after stop b0001 is %s", c.Tier)
		}
	}
}
