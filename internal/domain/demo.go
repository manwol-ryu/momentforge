package domain

import (
	"fmt"
	"sort"
)

// DemoDurationMs is the length of the offline demo timeline (10 minutes).
const DemoDurationMs = 600000

// DemoChat returns CzzEdit's offline demo chat: steady background talk,
// three group reactions (laughter at 5:00, a repeated phrase at 6:10,
// surprise at 7:30), and one author spamming laughter at 8:40, which the
// per-author cap must ignore. It is synthetic and contains no real users.
func DemoChat() []ChatEvent {
	type row struct {
		t      int64
		author string
		text   string
	}
	var rows []row
	for start := int64(0); start < DemoDurationMs; start += 10000 {
		for i := int64(0); i < 10; i++ {
			rows = append(rows, row{start + i*800, fmt.Sprintf("user-%d", i), fmt.Sprintf("평소 대화 %d", i)})
		}
	}
	for _, burst := range []struct {
		start int64
		text  string
	}{{300000, "ㅋㅋㅋㅋ"}, {370000, "다시 보고 싶은 장면"}, {450000, "헐"}} {
		for offset := int64(0); offset < 20000; offset += 2000 {
			for i := int64(0); i < 18; i++ {
				rows = append(rows, row{burst.start + offset + i*30, fmt.Sprintf("reaction-%d", i), burst.text})
			}
		}
	}
	for offset := int64(0); offset < 100; offset++ {
		rows = append(rows, row{520000 + offset*10, "spammer", "ㅋㅋㅋㅋ"})
	}

	sort.SliceStable(rows, func(i, j int) bool { return rows[i].t < rows[j].t })

	out := make([]ChatEvent, len(rows))
	for i, r := range rows {
		out[i] = ChatEvent{ID: fmt.Sprintf("demo-%05d", i+1), Seq: int64(i + 1), OffsetMs: r.t, AuthorKey: r.author, Text: r.text}
	}
	return out
}

// DemoCallbackChat is the planted cut-analysis case (docs/AI-CUT-ANALYSIS.md
// 8): at the laughter of 5:00 a few viewers call back to the penalty promise
// of DemoUtterances. They sit inside that busy minute, so the sample's
// candidates and per-minute counts stay those of CzzEdit's demo (DemoChat is
// kept identical to the legacy rows). The sample session stores both.
func DemoCallbackChat() []ChatEvent {
	rows := []struct {
		t      int64
		author string
		text   string
	}{{302000, "user-5", "아까 벌칙 건다며 ㅋㅋㅋ"}, {304000, "user-8", "약속 지켜"}, {307000, "user-2", "아까 그거 ㅋㅋ"},
		{311000, "user-3", "노래 가즈아"}}
	out := make([]ChatEvent, len(rows))
	for i, r := range rows {
		out[i] = ChatEvent{ID: fmt.Sprintf("demo-cb-%02d", i+1), OffsetMs: r.t, AuthorKey: r.author, Text: r.text}
	}
	return out
}

// DemoUtterances is what the sample's broadcaster says: steady talk every
// half minute, a penalty promise at 3:40 that the laughter at 5:00 calls
// back to, the boast and the failure before it, and the admission after.
// Synthetic; no real person spoke these lines.
func DemoUtterances() []Utterance {
	type line struct {
		start, end int64
		text       string
	}
	var lines []line
	for t := int64(5000); t < DemoDurationMs; t += 30000 {
		lines = append(lines, line{t, t + 4000, fmt.Sprintf("자, %d분 %d초 지점이고요, 계속 가 볼게요", t/60000, t%60000/1000)})
	}
	planted := []line{
		{220000, 226000, "이번 판 지면 벌칙으로 노래 부를게요. 약속"},
		{290000, 294000, "아 이건 안 죽는다니까요, 절대 안 죽어요"},
		{297000, 300000, "어 어 어… 죽었다"},
		{312000, 318000, "네… 벌칙 하겠습니다. 노래 준비할게요"},
		{366000, 370000, "다시 보고 싶으면 클립으로 남겨 둘게요"},
		{447000, 450000, "잠깐만요, 이거 뭐지"},
	}
	lines = append(lines, planted...)
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].start < lines[j].start })
	out := make([]Utterance, len(lines))
	for i, l := range lines {
		out[i] = Utterance{StartMs: l.start, EndMs: l.end, Speaker: "방송자", Text: l.text, Confidence: 0.9}
	}
	return out
}
