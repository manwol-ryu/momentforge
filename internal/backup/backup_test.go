package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `{"t":16999,"at":1791025326999,"type":"chat","status":"NORMAL","uid":"u1","nick":"제비","role":"common_user","msg":"{:grubbyBFhrii:}","emojis":{"grubbyBFhrii":"https://example.test/e.png"}}
{"t":40000,"at":1791025350000,"type":"system","status":"NORMAL","uid":null,"nick":null,"role":null,"msg":"📢 이모티콘 모드 OFF"}
not json
{"t":3694000,"at":1791029004000,"type":"donation","status":"NORMAL","uid":"anonymous","nick":null,"role":null,"msg":"화이팅","amount":1000,"anonymous":true,"donation_type":"CHAT"}
{"t":3700000,"at":1791029010000,"type":"subscription","status":"NORMAL","uid":"u2","nick":"소몽","role":"common_user","msg":"","months":3,"tier":"1"}
{"t":3600000,"at":1791028910000,"type":"chat","status":"CBOTBLIND","uid":"u3","nick":"x","role":"common_user","msg":"가려짐"}
`

func TestParseChatReadsEveryKindAndSortsByTime(t *testing.T) {
	recs, skipped, err := ParseChat(strings.NewReader(sample))
	if err != nil || skipped != 1 || len(recs) != 5 {
		t.Fatalf("recs %d skipped %d err %v", len(recs), skipped, err)
	}
	if recs[0].Kind != KindChat || recs[0].AuthorID != "u1" || recs[0].Emotes[0] != "grubbyBFhrii" || recs[0].Text != "{:grubbyBFhrii:}" {
		t.Errorf("chat = %+v", recs[0])
	}
	if recs[1].Kind != KindSystem || recs[1].AuthorID != "" {
		t.Errorf("system = %+v", recs[1])
	}
	if recs[2].Kind != KindChat || recs[2].Status != "CBOTBLIND" || recs[2].OffsetMs != 3600000 {
		t.Errorf("sorted = %+v", recs[2])
	}
	if recs[3].Kind != KindDonation || recs[3].Amount != 1000 || !recs[3].Anonymous || recs[3].AuthorID != "" {
		t.Errorf("donation = %+v", recs[3])
	}
	if recs[4].Kind != KindSubscription || recs[4].Amount != 3 || recs[4].AuthorID != "u2" {
		t.Errorf("subscription = %+v", recs[4])
	}
	if _, _, err := ParseChat(strings.NewReader("hello\n")); err != ErrFormat {
		t.Errorf("plain text: %v", err)
	}
}

func TestScanFindsBroadcastsWithTheirMedia(t *testing.T) {
	dir := t.TempDir()
	stem := "26_10_03 4부 방종"
	for _, f := range []string{stem + ".chat.jsonl", stem + ".m4a", stem + ".mp4", "26_10_01 음성 없음.chat.jsonl", "메모.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600)
	}
	list, err := Scan(dir)
	if err != nil || len(list) != 2 {
		t.Fatalf("list %+v err %v", list, err)
	}
	if list[0].Name != stem || list[0].Title != "4부 방종" || list[0].Date.Year() != 2026 || list[0].Date.Month() != 10 || list[0].Date.Day() != 3 ||
		list[0].AudioPath == "" || list[0].VideoPath == "" {
		t.Errorf("first = %+v", list[0])
	}
	if list[1].AudioPath != "" || list[1].VideoPath != "" {
		t.Errorf("second = %+v", list[1])
	}
	b, err := Locate(filepath.Join(dir, stem+".m4a"))
	if err != nil || b.ChatPath != filepath.Join(dir, stem+".chat.jsonl") {
		t.Errorf("locate = %+v err %v", b, err)
	}
	if title, date := ParseName("제목만"); title != "제목만" || !date.IsZero() {
		t.Errorf("ParseName = %q %v", title, date)
	}
}
