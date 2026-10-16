package chzzk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// fakeComments serves Naver-comment-shaped pages. All data is synthetic.
func fakeComments(t *testing.T, handler http.HandlerFunc) *Chzzk {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return New(WithCommentBase(srv.URL, u.Hostname()), WithInterval(0),
		WithSleep(func(ctx context.Context, d time.Duration) error { return ctx.Err() }))
}

func comment(id int, text string, extra map[string]any) map[string]any {
	row := map[string]any{"commentId": id, "content": text, "objectId": "1234", "parentCommentId": 0, "replyCount": 0,
		"deleted": false, "secret": false, "hideByCleanBot": false}
	for k, v := range extra {
		row[k] = v
	}
	return map[string]any{"comment": row, "user": map[string]any{"userIdHash": "u" + strconv.Itoa(id), "userNickname": "가짜"}}
}

// threadServer has 35 comments; #1 has 3 replies (1 inline, 2 by cursor), #2 is deleted.
func threadServer(t *testing.T, w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/1/replyComments") {
		if r.URL.Query().Get("offset") != "0" {
			t.Errorf("reply cursor %s", r.URL.Query().Get("offset"))
		}
		w.Write(content(map[string]any{"comments": map[string]any{"data": []any{
			comment(101, "12:00 여기", map[string]any{"parentCommentId": 1}),
			comment(102, "답글", map[string]any{"parentCommentId": 1}),
		}}}))
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/type/STREAMING_VIDEO/id/1234/comments") {
		t.Errorf("path %s", r.URL.Path)
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	var rows []any
	for id := offset + 1; id <= min(offset+30, 35); id++ {
		extra := map[string]any{}
		switch id {
		case 1:
			extra["replyCount"] = 3
		case 2:
			extra["deleted"] = true
		}
		c := comment(id, "1:23 웃긴 장면 "+strconv.Itoa(id), extra)
		if id == 1 {
			c["replyComments"] = []any{comment(100, "인라인 답글", map[string]any{"parentCommentId": 1})}
		}
		rows = append(rows, c)
	}
	w.Write(content(map[string]any{"commentActive": true, "bestComments": []any{comment(5, "1:23 웃긴 장면 5", nil)},
		"comments": map[string]any{"data": rows, "commentCount": 35}}))
}

func TestVODCommentsReadsPagesAndReplies(t *testing.T) {
	c := fakeComments(t, func(w http.ResponseWriter, r *http.Request) { threadServer(t, w, r) })
	var calls int
	res, err := c.VODComments(context.Background(), vod, 100, func(pages, n int) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Comments) != 38 || res.Pages != 3 || res.ReportedCount != 35 || len(res.Issues) != 0 || calls != 3 {
		t.Fatalf("comments=%d pages=%d reported=%d issues=%v progress=%d", len(res.Comments), res.Pages, res.ReportedCount, res.Issues, calls)
	}
	byID := map[string]int{}
	for i, cm := range res.Comments {
		byID[cm.ID] = i
	}
	if cm := res.Comments[byID["101"]]; cm.ParentID != "1" || cm.AuthorID != "u101" {
		t.Fatalf("reply %+v", cm)
	}
	if !res.Comments[byID["2"]].Hidden || res.Comments[byID["3"]].Hidden {
		t.Fatal("deleted flag")
	}
}

func TestVODCommentsBudgetAndPartialFailure(t *testing.T) {
	c := fakeComments(t, func(w http.ResponseWriter, r *http.Request) { threadServer(t, w, r) })
	res, err := c.VODComments(context.Background(), vod, 1, nil)
	if err != nil || len(res.Comments) != 31 || !strings.Contains(strings.Join(res.Issues, ","), "page_budget") {
		t.Fatalf("budget: %d comments, issues %v, err %v", len(res.Comments), res.Issues, err)
	}

	pages := 0
	c = fakeComments(t, func(w http.ResponseWriter, r *http.Request) {
		if pages++; pages > 1 {
			w.WriteHeader(503)
			return
		}
		threadServer(t, w, r)
	})
	res, err = c.VODComments(context.Background(), vod, 100, nil)
	if err != nil || len(res.Comments) != 31 || !strings.Contains(strings.Join(res.Issues, ","), "RETRY_EXHAUSTED") {
		t.Fatalf("partial: %d comments, issues %v, err %v", len(res.Comments), res.Issues, err)
	}
}

func TestVODCommentsRejectsChangedShapes(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{"no count", map[string]any{"comments": map[string]any{"data": []any{}}}, "COMMENT_SCHEMA"},
		{"disabled", map[string]any{"commentActive": false, "comments": map[string]any{"data": []any{}, "commentCount": 0}}, "COMMENTS_DISABLED"},
		{"bad row", map[string]any{"comments": map[string]any{"data": []any{map[string]any{"comment": map[string]any{"commentId": "x"}}}, "commentCount": 1}}, "COMMENT_SCHEMA"},
		{"other video", map[string]any{"comments": map[string]any{"data": []any{comment(1, "a", map[string]any{"objectId": "999"})}, "commentCount": 1}}, "COMMENT_SOURCE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fakeComments(t, func(w http.ResponseWriter, r *http.Request) { w.Write(content(tc.body)) })
			_, err := c.VODComments(context.Background(), vod, 10, nil)
			if ue, ok := domain.AsUserError(err); !ok || ue.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}
