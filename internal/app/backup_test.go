package app

import (
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/backup"
	"github.com/manwol-ryu/momentforge/internal/privacy"
)

// Imported chat keeps no original id or nickname: authors become the
// session's pseudonyms, anonymous donors stay anonymous, kinds and amounts
// come along, and repeated identical lines get distinct ids.
func TestBackupEventsArePseudonymous(t *testing.T) {
	salt, _ := privacy.NewSalt()
	recs := []backup.Record{
		{OffsetMs: 1000, Kind: backup.KindChat, AuthorID: "real-uid-1", Nick: "실명닉", Text: "안녕"},
		{OffsetMs: 1000, Kind: backup.KindChat, AuthorID: "real-uid-1", Nick: "실명닉", Text: "안녕"},
		{OffsetMs: 2000, Kind: backup.KindDonation, Anonymous: true, Text: "화이팅", Amount: 1000},
		{OffsetMs: 3000, Kind: backup.KindSubscription, AuthorID: "real-uid-2", Nick: "둘", Amount: 6},
		{OffsetMs: 4000, Kind: backup.KindChat, AuthorID: "real-uid-3", Text: "가려짐", Status: "CBOTBLIND"},
		{OffsetMs: 5000, Kind: backup.KindSystem, Text: "📢 알림"},
	}
	evs := backupEvents(salt, recs)
	if len(evs) != 6 || evs[0].ID == evs[1].ID || evs[0].AuthorKey != evs[1].AuthorKey {
		t.Fatalf("events %+v", evs)
	}
	for _, e := range evs {
		for _, secret := range []string{"real-uid", "실명닉", "둘"} {
			if strings.Contains(e.AuthorKey, secret) || strings.Contains(e.ID, secret) {
				t.Fatalf("original identity leaked into %+v", e)
			}
		}
	}
	if evs[0].Kind != "" || evs[0].AuthorKey != privacy.AuthorKey(salt, "real-uid-1") {
		t.Errorf("chat = %+v", evs[0])
	}
	if evs[2].Kind != "donation" || evs[2].Amount != 1000 || evs[2].AuthorKey != "" {
		t.Errorf("donation = %+v", evs[2])
	}
	if evs[3].Kind != "subscription" || evs[3].Amount != 6 || evs[3].AuthorKey == "" {
		t.Errorf("subscription = %+v", evs[3])
	}
	if !evs[4].Hidden || evs[5].Kind != "system" || evs[5].AuthorKey != "" {
		t.Errorf("hidden/system = %+v %+v", evs[4], evs[5])
	}
}
