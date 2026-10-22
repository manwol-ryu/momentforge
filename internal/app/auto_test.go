package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// taskAI explains each chunk's first candidate, fixes "가짜 발언" to
// "가짜 발화" in transcripts, recommends a model per task and records what
// it was sent.
type taskAI struct {
	mu     sync.Mutex
	chunks []ai.Chunk
	steps  []string // step name:model
	models []string // review models
}

func (p *taskAI) ID() string           { return "taskai" }
func (p *taskAI) Name() string         { return "작업 AI" }
func (p *taskAI) KeyEnv() string       { return "" }
func (p *taskAI) DefaultModel() string { return "general" }
func (p *taskAI) MaxOutputTokens() int { return ai.MaxOutputTokens }
func (p *taskAI) TaskModel(task string) string {
	return map[string]string{ai.TaskTranscript: "fast", ai.TaskExplain: "balanced"}[task]
}

func (p *taskAI) Review(ctx context.Context, key, model string, c ai.Chunk) (json.RawMessage, ai.Usage, error) {
	p.mu.Lock()
	p.chunks = append(p.chunks, c)
	p.models = append(p.models, model)
	p.mu.Unlock()
	recs := []any{}
	if len(c.Candidates) > 0 && len(c.Evidence) > 0 {
		ev := c.Evidence[0]
		recs = append(recs, map[string]any{"source_candidate_ids": []string{c.Candidates[0].ID}, "start_ms": ev.TimeMs,
			"end_ms": min(ev.TimeMs+1000, c.EndMs), "title": "웃음 반응", "rationale": "말 뒤에 채팅이 몰림",
			"evidence_refs": []string{ev.Ref}, "uncertainty": "영상은 확인하지 않음", "priority": "medium"})
	}
	out, _ := json.Marshal(map[string]any{"status": "ok", "recommendations": recs})
	return out, ai.Usage{InputTokens: 100, OutputTokens: 20}, nil
}

func (p *taskAI) Step(ctx context.Context, key, model string, s ai.StepRequest) (json.RawMessage, ai.Usage, error) {
	p.mu.Lock()
	p.steps = append(p.steps, s.Name+":"+model)
	p.mu.Unlock()
	if s.Name != "transcript_fix" {
		return p.Review(ctx, key, model, s.Input.(ai.Chunk))
	}
	fixes := []any{}
	for _, l := range s.Input.(ai.TranscriptChunk).Utterances {
		if strings.Contains(l.Text, "가짜 발언") {
			fixes = append(fixes, map[string]any{"id": l.ID, "text": strings.Replace(l.Text, "발언", "발화", 1)})
		}
	}
	out, _ := json.Marshal(map[string]any{"fixes": fixes})
	return out, ai.Usage{InputTokens: 10, OutputTokens: 5}, nil
}

// burstVOD is a 10-minute VOD with audio: a viewer says hello every 10
// seconds, and at 5:00 thirty viewers laugh at once (one candidate).
type burstVOD struct{ fakeAudio }

func (f *burstVOD) VODChats(ctx context.Context, t platform.Target, cursor, dur int64) (platform.ChatPage, error) {
	var msgs []platform.RawChat
	for ms := int64(1000); ms < f.duration; ms += 10_000 {
		msgs = append(msgs, platform.RawChat{OffsetMs: ms, AuthorID: fmt.Sprintf("v%d", ms/60_000), Text: "안녕"})
	}
	for i := range 30 {
		msgs = append(msgs, platform.RawChat{OffsetMs: 300_000 + int64(i)*200, AuthorID: fmt.Sprintf("r%d", i), Text: "ㅋㅋㅋㅋ"})
	}
	slices.SortFunc(msgs, func(x, y platform.RawChat) int { return cmp.Compare(x.OffsetMs, y.OffsetMs) })
	return platform.ChatPage{Messages: msgs, Finished: true, Raw: []byte(`{}`)}, nil
}

// speechEngine hears a line at the start and one just before the laughter.
type speechEngine struct{ fakeEngine }

func (e *speechEngine) Transcribe(ctx context.Context, audio string, opts asr.Options, progress func(asr.Progress)) ([]domain.Utterance, error) {
	return []domain.Utterance{{StartMs: 1000, EndMs: 2000, Speaker: "방송자", Text: "가짜 발언"},
		{StartMs: 297_000, EndMs: 299_000, Speaker: "방송자", Text: "가짜 발언 이거 보세요"}}, nil
}

// autoApp is an app with a VOD that streams audio, a ready engine and the
// step AI with a key, set up with s (the provider is the step AI).
func autoApp(t *testing.T, s Settings) (*App, *taskAI) {
	t.Helper()
	a, _ := newTestApp(t, &burstVOD{fakeAudio{fakeVOD: fakeVOD{duration: 600_000, pages: 1, failAt: -1}}})
	a.cfg.ASR.Register(&speechEngine{fakeEngine{id: "fake", ready: true}})
	p := &taskAI{}
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(p)
	a.initAI()
	if _, err := a.SetAIKey("taskai", "k", false); err != nil {
		t.Fatal(err)
	}
	s.AIProvider = "taskai"
	if _, err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	return a, p
}

func TestCollectTranscribesFixesAndExplainsWhenTurnedOn(t *testing.T) {
	a, p := autoApp(t, Settings{AutoAI: true, AutoTranscribe: true, AutoTranscriptFix: true})
	ctx := context.Background()
	if _, err := a.StartCollect(CollectRequest{URL: "fake:7"}); err != nil {
		t.Fatal(err)
	}
	st := wait(t, a)
	if st.Phase != "done" || !strings.Contains(st.Message, "방송 내용 인식") || !strings.Contains(st.Message, "AI 장면 설명") ||
		st.Warning != "" {
		t.Fatalf("job = %+v", st)
	}
	// The transcript was fixed in place: the text changed, what was heard is kept.
	v, ok, err := a.Transcript(ctx, st.SessionID)
	if err != nil || !ok || v.Fixed != 2 || v.FixedBy == "" {
		t.Fatalf("transcript %+v %v %v", v, ok, err)
	}
	utts, _ := a.Utterances(ctx, st.SessionID, 0, math.MaxInt32, 10)
	if len(utts) != 2 || utts[1].Text != "가짜 발화 이거 보세요" || utts[1].RawText != "가짜 발언 이거 보세요" {
		t.Fatalf("utterances %+v", utts)
	}
	// Each task used its own model; the explanation read the fixed speech with the chat.
	if len(p.steps) != 1 || p.steps[0] != "transcript_fix:fast" || len(p.models) == 0 || p.models[0] != "balanced" {
		t.Fatalf("steps %v, review models %v", p.steps, p.models)
	}
	speech := false
	for _, c := range p.chunks {
		for _, e := range c.Evidence {
			speech = speech || (strings.HasPrefix(e.Ref, "utt:") && e.Content == "가짜 발화 이거 보세요" && e.Speaker == "방송자")
		}
	}
	if !speech {
		t.Fatalf("no speech in the explanation's evidence: %+v", p.chunks)
	}
	// Every candidate has its explanation, and the runs are the automatic work's.
	tl, _ := a.Timeline(ctx, st.SessionID)
	explained := map[string]bool{}
	rules := 0
	for _, it := range tl.Items {
		if it.Source == SourceAI {
			for _, id := range it.SourceCandidateIDs {
				explained[id] = true
			}
		}
		if it.Source == SourceRule {
			rules++
		}
	}
	for _, it := range tl.Items {
		if it.Source == SourceRule && !explained[it.ID] {
			t.Fatalf("%s has no explanation (timeline %+v)", it.ID, tl.Items)
		}
	}
	if rules == 0 {
		t.Fatal("no candidates to explain")
	}
	runs, _ := a.cfg.Store.AIRuns(ctx, st.SessionID)
	if len(runs) < 2 {
		t.Fatalf("runs %+v", runs)
	}
	for _, r := range runs {
		if r.Origin != store.AIOriginAuto {
			t.Fatalf("run %s origin %q", r.ID, r.Origin)
		}
	}
	// A new analysis explains only what is new: the same moments keep theirs, nothing is sent.
	sent := len(p.models)
	if _, err := a.StartAnalyze(st.SessionID, false); err != nil {
		t.Fatal(err)
	}
	if st := wait(t, a); st.Phase != "done" || len(p.models) != sent {
		t.Fatalf("re-analysis %+v sent %d more", st, len(p.models)-sent)
	}
}

func TestAutoAIIsOffByDefaultAndKeepsItsLimit(t *testing.T) {
	// Off: the collection transcribes, but nothing goes to AI.
	a, p := autoApp(t, Settings{AutoTranscribe: true, AutoTranscriptFix: true})
	ctx := context.Background()
	if _, err := a.StartCollect(CollectRequest{URL: "fake:8"}); err != nil {
		t.Fatal(err)
	}
	st := wait(t, a)
	if st.Phase != "done" || !strings.Contains(st.Message, "방송 내용 인식") || len(p.steps)+len(p.models) != 0 {
		t.Fatalf("job %+v, sent %v %v", st, p.steps, p.models)
	}
	// The button sends within the session's limit: one request, then it stops and says why.
	if _, err := a.SaveSettings(Settings{AIProvider: "taskai", AutoTranscriptFix: true, AutoAIMaxRequests: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartAIReview(ctx, st.SessionID); err != nil {
		t.Fatal(err)
	}
	st = wait(t, a)
	if st.Phase != "done" || !strings.Contains(st.Warning, "한도") {
		t.Fatalf("review job %+v", st)
	}
	used, _ := a.cfg.Store.AIUsageOf(ctx, st.SessionID, store.AIOriginAuto)
	if used.Requests != 1 || len(p.steps)+len(p.models) != 1 {
		t.Fatalf("used %+v, sent %v %v", used, p.steps, p.models)
	}
	// The sample goes to AI only when the button asks for it (one candidate takes more than one request).
	if _, err := a.SaveSettings(Settings{AIProvider: "taskai", AutoTranscriptFix: true, AutoAIMaxRequests: 20}); err != nil {
		t.Fatal(err)
	}
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	before := len(p.steps) + len(p.models)
	if _, err := a.StartAIReview(ctx, demo.ID); err != nil {
		t.Fatalf("demo review: %v", err)
	}
	if st := wait(t, a); st.Phase != "done" || len(p.steps)+len(p.models) == before {
		t.Fatalf("demo review job %+v", st)
	}
}
