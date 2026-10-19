package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Transcripts (docs/AI-CUT-ANALYSIS.md 2.2): what the broadcaster said,
// recognised on the user's computer or read from a caption file. Only the
// newest transcript of a session is served.

type transcriptJSON struct {
	ID             string    `json:"transcript_id"`
	Source         string    `json:"source" jsonschema:"asr(이 컴퓨터의 음성 인식), file(사용자가 넣은 자막 파일), demo(샘플)"`
	Engine         string    `json:"engine" jsonschema:"음성 인식 엔진 (자막 파일이면 비어 있음)"`
	UtteranceCount int       `json:"utterance_count"`
	Coverage       []gapJSON `json:"coverage" jsonschema:"전사한 구간 (이 밖의 발언은 없음)"`
	CreatedAt      string    `json:"created_at"`
}

type utteranceJSON struct {
	Ref     string `json:"ref" jsonschema:"컷 분석 근거와 같은 ID (utt:로 시작)"`
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Start   string `json:"start" jsonschema:"H:MM:SS"`
	End     string `json:"end" jsonschema:"H:MM:SS"`
	Speaker string `json:"speaker" jsonschema:"화자 역할명 (방송자 등). 실제 이름이 아님"`
	Text    string `json:"text"`
}

type utterancesOut struct {
	Transcript transcriptJSON `json:"transcript"`
	pageOut
	Utterances []utteranceJSON `json:"utterances"`
}

func utterancesResult(p app.UtterancePage, offset int) utterancesOut {
	t := p.Transcript
	out := utterancesOut{Transcript: transcriptJSON{ID: t.ID, Source: t.Source, Engine: t.Engine, UtteranceCount: t.Utterances,
		Coverage: []gapJSON{}, CreatedAt: t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")}, Utterances: []utteranceJSON{}}
	for _, g := range t.Coverage {
		out.Transcript.Coverage = append(out.Transcript.Coverage, gapJSON{g.StartMs, g.EndMs, tc(g.StartMs), tc(g.EndMs)})
	}
	for _, u := range p.Utterances {
		out.Utterances = append(out.Utterances, utterance(u))
	}
	out.pageOut = makePage(p.Total, offset, len(out.Utterances))
	return out
}

func utterance(u domain.Utterance) utteranceJSON {
	return utteranceJSON{Ref: "utt:" + u.ID, StartMs: u.StartMs, EndMs: u.EndMs, Start: tc(u.StartMs), End: tc(u.EndMs),
		Speaker: u.Speaker, Text: u.Text}
}

func utterancesMarkdown(head string, out utterancesOut) string {
	var b strings.Builder
	t := out.Transcript
	src := map[string]string{"asr": "음성 인식", "file": "자막 파일", "demo": "샘플"}[t.Source]
	if src == "" {
		src = t.Source
	}
	fmt.Fprintf(&b, "# %s\n- 대본 %s (%s", head, t.ID, src)
	if t.Engine != "" {
		fmt.Fprintf(&b, ", %s", t.Engine)
	}
	fmt.Fprintf(&b, "), 발언 %d개\n", t.UtteranceCount)
	b.WriteString(pageLine(out.pageOut))
	for _, u := range out.Utterances {
		fmt.Fprintf(&b, "- %s~%s [%s] %s (%s)\n", u.Start, u.End, mdEscape(u.Speaker), mdEscape(u.Text), u.Ref)
	}
	return b.String()
}

// --- mf_search_utterances ---

type searchUtterancesIn struct {
	SessionID string `json:"session_id"`
	Query     string `json:"query" jsonschema:"찾을 글자 (1~100자). 발언에 이 글자가 들어 있으면 찾습니다"`
	Start     string `json:"start,omitempty" jsonschema:"이 시각부터 (예: 0:10:00)"`
	End       string `json:"end,omitempty" jsonschema:"이 시각 전까지"`
	pageIn
	formatIn
}

func (t *tools) searchUtterances(ctx context.Context, in searchUtterancesIn) (utterancesOut, string, error) {
	offset, limit, err := in.bounds(50, 200)
	if err != nil {
		return utterancesOut{}, "", err
	}
	from, to, err := span(in.Start, in.End)
	if err != nil {
		return utterancesOut{}, "", err
	}
	p, err := t.r.SearchUtterances(ctx, in.SessionID, in.Query, from, to, offset, limit)
	if err != nil {
		return utterancesOut{}, "", err
	}
	out := utterancesResult(p, offset)
	text, err := in.render(out, func() string {
		return utterancesMarkdown(fmt.Sprintf("발언 검색 \"%s\"", mdEscape(in.Query)), out)
	})
	return out, text, err
}

// --- mf_get_transcript ---

type transcriptIn struct {
	SessionID string `json:"session_id"`
	Start     string `json:"start,omitempty" jsonschema:"이 시각부터 (예: 1:02:00, 비우면 처음부터)"`
	End       string `json:"end,omitempty" jsonschema:"이 시각 전까지 (비우면 끝까지)"`
	pageIn
	formatIn
}

func (t *tools) getTranscript(ctx context.Context, in transcriptIn) (utterancesOut, string, error) {
	offset, limit, err := in.bounds(100, 500)
	if err != nil {
		return utterancesOut{}, "", err
	}
	from, to, err := span(in.Start, in.End)
	if err != nil {
		return utterancesOut{}, "", err
	}
	p, err := t.r.UtterancesInRange(ctx, in.SessionID, from, to, offset, limit)
	if err != nil {
		return utterancesOut{}, "", err
	}
	out := utterancesResult(p, offset)
	text, err := in.render(out, func() string {
		head := "방송 발언"
		if in.Start != "" || in.End != "" {
			end := "끝"
			if in.End != "" {
				end = tc(to)
			}
			head += fmt.Sprintf(" %s~%s", tc(from), end)
		}
		return utterancesMarkdown(head, out)
	})
	return out, text, err
}
