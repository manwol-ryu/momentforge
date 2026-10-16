package chzzk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// commentEndpoint is the Naver comment API behind CHZZK's video page (not
// the LIVE chat API). It has its own host, so it has its own client.
type commentEndpoint struct {
	base string
	http *platform.HTTPClient
}

const commentPageSize = 30

func commentErr(code, msg string) error { return domain.NewUserError(code, msg) }

func (c *Chzzk) getComments(ctx context.Context, path string, q url.Values) (map[string]any, error) {
	res, err := c.comments.http.Get(ctx, platform.QueryURL(c.comments.base, path, q))
	if err != nil {
		return nil, err
	}
	content, _, err := decodeContent(res)
	return content, err
}

// VODComments reads every public comment and reply of a VOD the way
// CzzEdit did: 30 per page in ascending order, then each thread's replies
// by cursor. Errors after some comments were read become issues.
func (c *Chzzk) VODComments(ctx context.Context, t platform.Target, maxPages int, progress func(pages, comments int)) (platform.CommentResult, error) {
	path := "/type/STREAMING_VIDEO/id/" + t.ID + "/comments"
	res := platform.CommentResult{ReportedCount: -1}
	index := map[string]int{}
	replyCount := map[string]int{}

	var add func(items []any) error
	add = func(items []any) error {
		for _, it := range items {
			item, _ := it.(map[string]any)
			row, _ := item["comment"].(map[string]any)
			id, idOK := intField(row["commentId"])
			text, textOK := row["content"].(string)
			if !idOK || !textOK {
				return commentErr("COMMENT_SCHEMA", "영상 댓글 형식이 변경되었습니다.")
			}
			if obj, ok := row["objectId"]; ok && obj != nil && fmt.Sprint(obj) != t.ID {
				return commentErr("COMMENT_SOURCE", "다른 영상의 댓글이 반환되었습니다.")
			}
			cm := platform.Comment{ID: strconv.FormatInt(id, 10), Text: text,
				Hidden: truthy(row["deleted"]) || truthy(row["secret"]) || truthy(row["hideByCleanBot"])}
			if p, ok := intField(row["parentCommentId"]); ok && p != 0 && p != id {
				cm.ParentID = strconv.FormatInt(p, 10)
			}
			if user, ok := item["user"].(map[string]any); ok {
				cm.AuthorID, _ = user["userIdHash"].(string)
			}
			if n, ok := intField(row["replyCount"]); ok {
				replyCount[cm.ID] = int(n)
			}
			if i, seen := index[cm.ID]; seen {
				res.Comments[i] = cm
			} else {
				index[cm.ID] = len(res.Comments)
				res.Comments = append(res.Comments, cm)
			}
			if replies, ok := item["replyComments"].([]any); ok {
				if err := add(replies); err != nil {
					return err
				}
			}
		}
		return nil
	}
	report := func() {
		if progress != nil {
			progress(res.Pages, len(res.Comments))
		}
	}

	err := func() error {
		finished := false
		for page := 0; page < maxPages; page++ {
			data, err := c.getComments(ctx, path, url.Values{"limit": {strconv.Itoa(commentPageSize)},
				"offset": {strconv.Itoa(page * commentPageSize)}, "orderType": {"ASC"}, "pagingType": {"PAGE"}})
			if err != nil {
				return err
			}
			block, _ := data["comments"].(map[string]any)
			rows, rowsOK := block["data"].([]any)
			total, totalOK := intField(block["commentCount"])
			if !rowsOK || !totalOK {
				return commentErr("COMMENT_SCHEMA", "영상 댓글 목록 형식이 변경되었습니다.")
			}
			if active, isBool := data["commentActive"].(bool); isBool && !active {
				return commentErr("COMMENTS_DISABLED", "영상 댓글이 비활성화되어 있습니다.")
			}
			res.ReportedCount = int(total)
			before := len(res.Comments)
			if best, ok := data["bestComments"].([]any); ok {
				if err := add(best); err != nil {
					return err
				}
			}
			if err := add(rows); err != nil {
				return err
			}
			res.Pages++
			report()
			if int64(page+1)*commentPageSize >= total {
				finished = true
				break
			}
			if len(rows) == 0 || len(res.Comments) == before {
				return commentErr("COMMENT_STALLED", "댓글 페이지가 진행되지 않습니다.")
			}
		}
		if !finished {
			res.Issues = append(res.Issues, "page_budget")
		}
		// Replies come by cursor: the last reply's commentId, not a page number.
		for _, parent := range append([]platform.Comment(nil), res.Comments...) {
			expected := replyCount[parent.ID]
			if parent.ParentID != "" || expected == 0 {
				continue
			}
			children := func() int {
				n := 0
				for _, cm := range res.Comments {
					if cm.ParentID == parent.ID {
						n++
					}
				}
				return n
			}
			cursor := int64(0)
			for children() < expected && res.Pages < maxPages {
				data, err := c.getComments(ctx, path+"/"+parent.ID+"/replyComments", url.Values{"offset": {strconv.FormatInt(cursor, 10)}})
				if err != nil {
					return err
				}
				block, _ := data["comments"].(map[string]any)
				rows, _ := block["data"].([]any)
				if len(rows) == 0 {
					res.Issues = append(res.Issues, "reply_incomplete")
					break
				}
				if err := add(rows); err != nil {
					return err
				}
				res.Pages++
				report()
				last, _ := rows[len(rows)-1].(map[string]any)
				lastRow, _ := last["comment"].(map[string]any)
				next, _ := intField(lastRow["commentId"])
				if next == cursor {
					res.Issues = append(res.Issues, "reply_stalled")
					break
				}
				cursor = next
			}
			if children() < expected {
				res.Issues = append(res.Issues, "reply_incomplete")
			}
		}
		return nil
	}()
	if err != nil {
		if len(res.Comments) == 0 {
			return res, err
		}
		code := "COMMENT_ERROR"
		if ue, ok := domain.AsUserError(err); ok {
			code = ue.Code
		}
		res.Issues = append(res.Issues, code)
	}
	return res, nil
}

func intField(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case float64:
		return int64(n), n == float64(int64(n))
	}
	return 0, false
}
