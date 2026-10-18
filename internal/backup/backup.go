// Package backup reads the NAS backups that chzzk-automation writes
// (docs/AI-CUT-ANALYSIS.md 2.5a): per broadcast a YY_MM_DD 제목.mp4, the same
// audio as .m4a, and the chat as .chat.jsonl (one record per line). It only
// parses; the app pseudonymises authors and stores nothing it does not need
// (no original ids, nicknames or emote URLs leave this package).
package backup

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Record kinds, as the recorder names them.
const (
	KindChat         = "chat"
	KindDonation     = "donation"
	KindSubscription = "subscription"
	KindSystem       = "system"
)

// Record is one line of a .chat.jsonl: a chat message, a donation, a
// subscription or a system notice. AuthorID and Nick are the platform's
// own; they must be pseudonymised before anything is stored.
type Record struct {
	OffsetMs  int64  // t: position in the video
	AtMs      int64  // at: wall-clock time (epoch ms)
	Kind      string // chat | donation | subscription | system
	Status    string // NORMAL | CBOTBLIND (hidden by the bot) …
	AuthorID  string // uid; "" for anonymous or system
	Nick      string
	Role      string // common_user | streamer | … ("" when none)
	Text      string
	Amount    int64 // donation: won; subscription: months
	Anonymous bool
	Emotes    []string // emote codes in the text ({:name:}), without their URLs
}

// Broadcast is one recorded broadcast in a backup folder.
type Broadcast struct {
	Name       string    // file stem, e.g. "26_10_03 4부 방종"
	Title      string    // without the date prefix
	Date       time.Time // from the prefix (zero when there is none)
	ChatPath   string    // .chat.jsonl (required)
	AudioPath  string    // .m4a when present
	VideoPath  string    // .mp4 when present
	ChatBytes  int64
	AudioBytes int64
	VideoBytes int64
}

var namePrefix = regexp.MustCompile(`^(\d{2})_(\d{2})_(\d{2}) (.+)$`)

// ParseName splits "YY_MM_DD 제목" into its date and title.
func ParseName(stem string) (title string, date time.Time) {
	m := namePrefix.FindStringSubmatch(stem)
	if m == nil {
		return stem, time.Time{}
	}
	var y, mo, d int
	fmt.Sscanf(m[1]+" "+m[2]+" "+m[3], "%d %d %d", &y, &mo, &d)
	return m[4], time.Date(2000+y, time.Month(mo), d, 0, 0, 0, 0, time.Local)
}

// Scan lists the broadcasts in a folder: every .chat.jsonl with the media
// of the same name beside it. Subfolders are not searched (the recorder
// keeps one folder per month).
func Scan(dir string) ([]Broadcast, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Broadcast
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".chat.jsonl") || strings.HasPrefix(name, ".") {
			continue
		}
		stem := strings.TrimSuffix(name, ".chat.jsonl")
		b := Broadcast{Name: stem, ChatPath: filepath.Join(dir, name)}
		b.Title, b.Date = ParseName(stem)
		if info, err := e.Info(); err == nil {
			b.ChatBytes = info.Size()
		}
		if info, err := os.Stat(filepath.Join(dir, stem+".m4a")); err == nil && !info.IsDir() {
			b.AudioPath, b.AudioBytes = filepath.Join(dir, stem+".m4a"), info.Size()
		}
		if info, err := os.Stat(filepath.Join(dir, stem+".mp4")); err == nil && !info.IsDir() {
			b.VideoPath, b.VideoBytes = filepath.Join(dir, stem+".mp4"), info.Size()
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name }) // newest first
	return out, nil
}

// Locate finds a broadcast from the path of any of its files.
func Locate(path string) (Broadcast, error) {
	dir, base := filepath.Split(path)
	stem := base
	for _, suffix := range []string{".chat.jsonl", ".chat.txt", ".m4a", ".mp4"} {
		if strings.HasSuffix(base, suffix) {
			stem = strings.TrimSuffix(base, suffix)
			break
		}
	}
	list, err := Scan(filepath.Clean(dir))
	if err != nil {
		return Broadcast{}, err
	}
	for _, b := range list {
		if b.Name == stem {
			return b, nil
		}
	}
	return Broadcast{}, domain.NewUserError("NOT_FOUND", "그 폴더에서 같은 이름의 .chat.jsonl을 찾지 못했습니다.")
}

// ErrFormat says the file is not the recorder's chat format.
var ErrFormat = errors.New("not a chzzk-automation chat file")

const maxLine = 1 << 20

// ParseChat reads a .chat.jsonl. Lines that are not JSON records are
// skipped and counted; a file with no record at all is ErrFormat.
func ParseChat(r io.Reader) (records []Record, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw struct {
			T         *int64            `json:"t"`
			At        int64             `json:"at"`
			Type      string            `json:"type"`
			Status    string            `json:"status"`
			UID       *string           `json:"uid"`
			Nick      *string           `json:"nick"`
			Role      *string           `json:"role"`
			Msg       *string           `json:"msg"`
			Emojis    map[string]string `json:"emojis"`
			Amount    *int64            `json:"amount"`
			Anonymous bool              `json:"anonymous"`
			Months    *int64            `json:"months"`
		}
		if json.Unmarshal([]byte(line), &raw) != nil || raw.T == nil || raw.Type == "" {
			skipped++
			continue
		}
		rec := Record{OffsetMs: *raw.T, AtMs: raw.At, Kind: raw.Type, Status: raw.Status, Anonymous: raw.Anonymous}
		if raw.UID != nil && *raw.UID != "anonymous" {
			rec.AuthorID = *raw.UID
		}
		if raw.Nick != nil {
			rec.Nick = *raw.Nick
		}
		if raw.Role != nil {
			rec.Role = *raw.Role
		}
		if raw.Msg != nil {
			rec.Text = *raw.Msg
		}
		switch raw.Type {
		case KindDonation:
			if raw.Amount != nil {
				rec.Amount = *raw.Amount
			}
		case KindSubscription:
			if raw.Months != nil {
				rec.Amount = *raw.Months
			}
		case KindChat, KindSystem:
		default:
			rec.Kind = KindSystem
		}
		for code := range raw.Emojis {
			rec.Emotes = append(rec.Emotes, code)
		}
		sort.Strings(rec.Emotes)
		records = append(records, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, skipped, err
	}
	if len(records) == 0 {
		return nil, skipped, ErrFormat
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].OffsetMs < records[j].OffsetMs })
	return records, skipped, nil
}
