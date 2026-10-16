package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// fakeCommentVOD is fakeVOD plus synthetic comments.
type fakeCommentVOD struct {
	*fakeVOD
	comments []platform.Comment
}

func (f *fakeCommentVOD) VODComments(ctx context.Context, t platform.Target, maxPages int, progress func(int, int)) (platform.CommentResult, error) {
	progress(1, len(f.comments))
	return platform.CommentResult{Comments: f.comments, ReportedCount: len(f.comments), Pages: 1}, nil
}

func TestCommentsOnlyThenPaste(t *testing.T) {
	src := &fakeCommentVOD{fakeVOD: &fakeVOD{duration: 600000, pages: 1, failAt: -1}, comments: []platform.Comment{
		{ID: "1", AuthorID: "viewer-a", Text: "1:23 웃긴 장면 / 2:00~2:30 하이라이트"},
		{ID: "2", AuthorID: "viewer-b", Text: "오후 3:00에 봤어요"},
		{ID: "3", AuthorID: "viewer-c", Text: "4:00 비밀 댓글", Hidden: true},
		{ID: "4", ParentID: "1", AuthorID: "viewer-a", Text: "ㅇㅈ"},
	}}
	a, _ := newTestApp(t, src.fakeVOD)
	a.cfg.Platforms = &platform.Registry{}
	a.cfg.Platforms.Register(src)
	ctx := context.Background()

	if _, err := a.StartComments(ctx, CommentRequest{URL: "fake:9"}); err != nil {
		t.Fatal(err)
	}
	st := wait(t, a)
	if st.Phase != "done" || st.Recommendations != 2 || st.SessionID == "" {
		t.Fatalf("job %+v", st)
	}
	info, _ := a.Session(ctx, st.SessionID)
	if info.State != store.StateCommentsOnly || !info.CanResume || info.ChatCount != 0 {
		t.Fatalf("comments-only session %+v", info)
	}
	v, err := a.Comments(ctx, st.SessionID)
	if err != nil || v == nil {
		t.Fatalf("comments: %v", err)
	}
	if v.Source != "website" || v.State != "finished" || v.CommentCount != 4 || len(v.Recommendations) != 2 {
		t.Fatalf("view %+v", v)
	}
	r0, r1 := v.Recommendations[0], v.Recommendations[1]
	if r0.StartMs != 83000 || r0.Description != "웃긴 장면" || r1.StartMs != 120000 || r1.EndMs != 150000 || !strings.Contains(r0.WatchURL, "t=83") {
		t.Fatalf("recommendations %+v", v.Recommendations)
	}
	if len(v.NeedsReview) != 1 || v.NeedsReview[0].CommentID != "2" || v.NeedsReview[0].Reasons[0] != "needs_review" {
		t.Fatalf("needs review %+v", v.NeedsReview)
	}
	files, _ := filepath.Glob(filepath.Join(a.exportDir(st.SessionID), "comments", v.CaptureID, "*"))
	if len(files) != 3 {
		t.Fatalf("files %v", files)
	}
	jsonl, _ := os.ReadFile(filepath.Join(a.exportDir(st.SessionID), "comments", v.CaptureID, "comments.jsonl"))
	if strings.Contains(string(jsonl), "viewer-") || strings.Contains(string(jsonl), "비밀") {
		t.Fatal("comments.jsonl holds an original author id or a hidden comment")
	}

	tl, err := a.Timeline(ctx, st.SessionID)
	if err != nil || len(tl.Items) != 2 || tl.Items[0].Source != SourceViewer || tl.Items[0].AnchorMs != 83000 || tl.Items[0].ClipEndMs != 113000 ||
		tl.Items[1].ClipEndMs != 150000 || tl.CaptureID != v.CaptureID {
		t.Fatalf("timeline %+v %v", tl, err)
	}
	txt, _ := os.ReadFile(filepath.Join(a.exportDir(st.SessionID), "timeline", "timeline.txt"))
	if !strings.Contains(string(txt), "00:01:23 [시청자 추천] 웃긴 장면") {
		t.Fatalf("timeline.txt:\n%s", txt)
	}

	// Asking again for the same video reuses its session; pasted text is a new capture.
	if _, err := a.StartComments(ctx, CommentRequest{URL: "fake:9", Text: "5:00 붙여넣은 시각"}); err != nil {
		t.Fatal(err)
	}
	if st2 := wait(t, a); st2.SessionID != st.SessionID || st2.Recommendations != 1 {
		t.Fatalf("paste job %+v", st2)
	}
	v, _ = a.Comments(ctx, st.SessionID)
	if v.Source != "manual_paste" || v.State != "partial" || len(v.Recommendations) != 1 {
		t.Fatalf("paste view %+v", v)
	}

	if _, err := a.StartComments(ctx, CommentRequest{SessionID: st.SessionID, Text: strings.Repeat("가", 12001)}); !isCode(err, "INPUT") {
		t.Fatalf("long paste: %v", err)
	}
	if _, err := a.StartComments(ctx, CommentRequest{}); !isCode(err, "INPUT") {
		t.Fatalf("no target: %v", err)
	}
}
