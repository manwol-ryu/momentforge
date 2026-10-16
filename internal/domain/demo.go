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
