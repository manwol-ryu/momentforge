package chzzk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// schema returns a user-facing SCHEMA_CHANGED error wrapping ErrSchemaChanged.
func schema(msg string) error {
	return domain.WrapUserError("SCHEMA_CHANGED", msg, platform.ErrSchemaChanged)
}

// get calls a /service/ endpoint and returns its "content" object and the raw body.
func (c *Chzzk) get(ctx context.Context, path string, q url.Values) (map[string]any, []byte, error) {
	if !strings.HasPrefix(path, "/service/") {
		return nil, nil, domain.NewUserError("INVALID_PATH", "허용되지 않은 요청입니다.")
	}
	res, err := c.http.Get(ctx, platform.QueryURL(c.base, path, q))
	if err != nil {
		return nil, nil, err
	}
	return decodeContent(res)
}

// decodeContent checks a CHZZK/Naver response envelope ({"code":200,"content":{…}}).
func decodeContent(res *platform.Response) (map[string]any, []byte, error) {
	switch {
	case res.Status == 401:
		return nil, res.Body, loginRequired()
	case res.Status == 403 || res.Status == 404:
		return nil, res.Body, domain.NewUserError("ACCESS_DENIED", "영상 또는 채팅에 접근할 수 없습니다. 시청 권한·삭제 여부를 확인해 주세요.")
	case res.Status != 200:
		return nil, res.Body, domain.NewUserError("HTTP_ERROR", fmt.Sprintf("채팅 서버가 HTTP %d을 반환했습니다.", res.Status))
	}
	var payload map[string]any
	dec := json.NewDecoder(bytes.NewReader(res.Body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, res.Body, schema("서버가 올바른 JSON을 반환하지 않았습니다.")
	}
	code := fmt.Sprint(payload["code"])
	switch code {
	case "401":
		return nil, res.Body, loginRequired()
	case "403", "404":
		return nil, res.Body, domain.NewUserError("ACCESS_DENIED", "이 계정으로 영상 또는 채팅을 열람할 수 없습니다.")
	}
	content, ok := payload["content"].(map[string]any)
	if code != "200" || !ok {
		return nil, res.Body, schema("서버 응답 내용이 예상한 형식과 다릅니다.")
	}
	return content, res.Body, nil
}

// Signed-in VOD access is on hold (REDESIGN.md 13).
func loginRequired() error {
	return domain.NewUserError("LOGIN_REQUIRED", "로그인이 필요한 다시보기는 아직 지원하지 않습니다. 공개 다시보기 주소로 시도해 주세요.")
}

// VODMeta reads title and duration (seconds in [0, 31 days]).
func (c *Chzzk) VODMeta(ctx context.Context, t platform.Target) (platform.VODMeta, error) {
	data, _, err := c.get(ctx, "/service/v3/videos/"+t.ID, nil)
	if err != nil {
		return platform.VODMeta{}, err
	}
	return parseMeta(data, t.ID)
}

func parseMeta(data map[string]any, id string) (platform.VODMeta, error) {
	dur, ok := number(data["duration"])
	title, titleOK := data["videoTitle"].(string)
	if !ok || dur <= 0 || dur > 31*24*3600 || !titleOK {
		return platform.VODMeta{}, schema("영상 제목 또는 길이를 확인할 수 없습니다.")
	}
	if no, present := data["videoNo"]; present && no != nil && fmt.Sprint(no) != id {
		return platform.VODMeta{}, domain.NewUserError("SOURCE_CHANGED", "요청한 영상과 응답 영상이 다릅니다.")
	}
	if enabled, isBool := data["videoChatEnabled"].(bool); isBool && !enabled {
		return platform.VODMeta{}, domain.NewUserError("CHAT_UNAVAILABLE", "이 다시보기는 채팅 제공이 꺼져 있습니다.")
	}
	return platform.VODMeta{Title: title, DurationMs: roundHalfEven(dur * 1000)}, nil
}

// VODChats fetches the page at cursor (playerMessageTime) and validates its
// time boundaries the way CzzEdit did. Rejected pages come back as PageError.
func (c *Chzzk) VODChats(ctx context.Context, t platform.Target, cursor, durationMs int64) (platform.ChatPage, error) {
	q := url.Values{"playerMessageTime": {strconv.FormatInt(cursor, 10)}, "previousVideoChatSize": {"50"}}
	data, raw, err := c.get(ctx, "/service/v1/videos/"+t.ID+"/chats", q)
	if err != nil {
		return platform.ChatPage{}, err
	}
	page, err := parsePage(data, cursor, durationMs)
	if err != nil {
		return platform.ChatPage{}, &platform.PageError{Raw: raw, Err: err}
	}
	page.Raw = raw
	return page, nil
}

func parsePage(data map[string]any, cursor, durationMs int64) (platform.ChatPage, error) {
	previous, okPrev := data["previousVideoChats"].([]any)
	following, okNext := data["videoChats"].([]any)
	nextRaw, hasNext := data["nextPlayerMessageTime"]
	if !okPrev || !okNext || !hasNext {
		return platform.ChatPage{}, schema("다시보기 채팅 페이지 형식이 변경되었습니다.")
	}
	var page platform.ChatPage
	var followTimes []int64
	for side, rows := range [2][]any{previous, following} {
		for _, r := range rows {
			row, ok := r.(map[string]any)
			if !ok {
				return page, schema("채팅 항목이 객체가 아닙니다.")
			}
			t, tOK := integer(row["playerMessageTime"])
			text, textOK := row["content"].(string)
			author, authorOK := "", true
			if a, present := row["userIdHash"]; present && a != nil {
				author, authorOK = a.(string)
			}
			if !tOK || t < 0 || t >= durationMs+1000 || !textOK || !authorOK {
				return page, schema("채팅 시각·본문·작성자 필드를 해석하지 못했습니다.")
			}
			if (side == 0 && t > cursor) || (side == 1 && t <= cursor) {
				return page, domain.WrapUserError("BOUNDARY_CHANGED",
					"채팅 페이지의 시간 경계가 예상과 다릅니다. 원문을 보존하고 중단했습니다.", platform.ErrSchemaChanged)
			}
			page.Messages = append(page.Messages, platform.RawChat{OffsetMs: t, AuthorID: author, Text: text})
			if side == 1 {
				followTimes = append(followTimes, t)
			}
		}
	}
	if nextRaw == nil {
		page.Finished = true
	} else {
		next, ok := integer(nextRaw)
		if !ok || next < cursor {
			return page, schema("다음 조회 위치가 올바르지 않습니다.")
		}
		if len(followTimes) == 0 {
			return page, domain.NewUserError("PAGE_STALLED", "빈 채팅 페이지에 다음 위치가 있어 수집을 보류했습니다.")
		}
		if next <= cursor || next != maxOf(followTimes) {
			return page, domain.NewUserError("PAGE_STALLED", "다음 채팅 위치가 진행되지 않거나 페이지와 일치하지 않습니다.")
		}
		page.Next = next
	}
	// A timestamp cursor cannot certify more than one page of simultaneous
	// messages; keep the data and mark the instant as a gap.
	if len(followTimes) >= 200 && allEqual(followTimes) {
		page.Gaps = append(page.Gaps, domain.Gap{StartMs: followTimes[0], EndMs: followTimes[0] + 1})
	}
	return page, nil
}

func number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

// integer accepts JSON integers only (Python: type(t) is int).
func integer(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok || strings.ContainsAny(n.String(), ".eE") {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

// roundHalfEven matches Python's round().
func roundHalfEven(f float64) int64 {
	i := int64(f)
	diff := f - float64(i)
	switch {
	case diff > 0.5, diff == 0.5 && i%2 != 0:
		return i + 1
	default:
		return i
	}
}

func maxOf(v []int64) int64 {
	m := v[0]
	for _, x := range v[1:] {
		m = max(m, x)
	}
	return m
}

func allEqual(v []int64) bool {
	for _, x := range v[1:] {
		if x != v[0] {
			return false
		}
	}
	return true
}
