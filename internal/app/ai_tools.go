package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// The cut analysis's search tools (docs/AI-CUT-ANALYSIS.md 3.2). The model
// asks, the server reads the same session's utterances and chat and adds
// what it finds to the candidate's input; nothing is read outside the
// session, and the budget caps rounds, requests per round and bytes.

// cutSearcher runs one candidate's search requests against the store.
type cutSearcher struct {
	a            *App
	sid          string
	transcriptID string // "" without a transcript
	maxSeq       int64
	endMs        int64
	budget       cut.Budget
	use          cut.BudgetUse
}

// run executes one round of requests (at most MaxRequestsPerRound) and
// merges what they find into in and known. It returns the results and the
// refs added this round.
func (s *cutSearcher) run(ctx context.Context, round int, reqs []cut.SearchRequest, in *cut.Input, known map[string]cut.Evidence) ([]cut.SearchResult, []string, error) {
	var results []cut.SearchResult
	var added []string
	for i, raw := range reqs {
		res := cut.SearchResult{Request: raw, Round: round, FoundRefs: []string{}}
		if i >= s.budget.MaxRequestsPerRound {
			res.Note = fmt.Sprintf("한 단계에 요청 %d개까지만 실행", s.budget.MaxRequestsPerRound)
			results = append(results, res)
			s.use.Exhausted = true
			continue
		}
		req, why := cut.Clamp(raw, *in)
		res.Request = req
		if why != "" {
			res.Note = why
			results = append(results, res)
			continue
		}
		if repeated(in.Searched, req) {
			res.Note = "같은 요청을 이미 실행함"
			results = append(results, res)
			continue
		}
		if s.use.Bytes >= s.budget.MaxBytes {
			res.Note = "검색으로 더할 수 있는 글의 상한에 닿음"
			s.use.Exhausted = true
			results = append(results, res)
			continue
		}
		s.use.Requests++
		s.use.Rounds = round // a round counts once something in it ran
		res.Ran = true
		found, activity, err := s.execute(ctx, req)
		if err != nil {
			return results, added, err
		}
		res.Activity = activity
		// Only what the model has not seen counts against the budget.
		var fresh []cut.Evidence
		for _, e := range found {
			if _, ok := known[e.Ref]; ok {
				continue
			}
			if s.use.Bytes+res.Bytes+len(e.Text) > s.budget.MaxBytes {
				res.Note = "검색으로 더할 수 있는 글의 상한에 닿아 일부만 가져옴"
				s.use.Exhausted = true
				break
			}
			res.Bytes += len(e.Text)
			fresh = append(fresh, e)
		}
		res.FoundRefs = cut.Merge(in, known, fresh)
		if res.FoundRefs == nil {
			res.FoundRefs = []string{}
		}
		s.use.Bytes += res.Bytes
		added = append(added, res.FoundRefs...)
		if len(res.FoundRefs) == 0 && res.Note == "" && len(activity) == 0 {
			res.Note = "새로 찾은 근거 없음"
		}
		results = append(results, res)
	}
	in.Searched = append(slices.Clip(in.Searched), results...)
	return results, added, nil
}

func repeated(done []cut.SearchResult, r cut.SearchRequest) bool {
	for _, d := range done {
		if d.Ran && cut.SameRequest(d.Request, r) {
			return true
		}
	}
	return false
}

// searchChatShare is how many of a search's results stay for chat when it
// looks in both utterances and chat: utterances come first, but a viewer's
// "아까 그거" is often what names the past event.
const searchChatShare = 10

// execute runs one clamped request.
func (s *cutSearcher) execute(ctx context.Context, r cut.SearchRequest) ([]cut.Evidence, []cut.ActivityPoint, error) {
	switch r.Tool {
	case cut.ToolSearch:
		return s.search(ctx, r)
	case cut.ToolReadAround:
		return s.read(ctx, max(0, r.AtMs-r.WindowS*1000), min(s.endMs, r.AtMs+r.WindowS*1000))
	case cut.ToolReadRange:
		return s.read(ctx, r.FromMs, min(s.endMs, r.ToMs))
	case cut.ToolActivity:
		bucket := r.WindowS * 1000
		buckets, err := s.a.cfg.Store.ChatActivity(ctx, s.sid, bucket, r.FromMs, r.ToMs)
		if err != nil {
			return nil, nil, err
		}
		points := make([]cut.ActivityPoint, 0, len(buckets))
		for _, b := range buckets {
			points = append(points, cut.ActivityPoint{StartMs: b.Index * bucket, Chats: b.Chats, Authors: b.Authors})
		}
		return nil, points, nil
	}
	return nil, nil, nil
}

// search finds the matches nearest before r.BeforeMs, at most
// cut.SearchMaxResults: utterances first, with room kept for chat when the
// request looks in both (searchChatShare), in time order within each kind.
func (s *cutSearcher) search(ctx context.Context, r cut.SearchRequest) ([]cut.Evidence, []cut.ActivityPoint, error) {
	var utts []domain.Utterance
	var chats []domain.ChatEvent
	var err error
	if r.In != "chats" && s.transcriptID != "" {
		if utts, err = s.a.cfg.Store.SearchUtterances(ctx, s.transcriptID, r.Query, 0, r.BeforeMs, cut.SearchMaxResults); err != nil {
			return nil, nil, err
		}
	}
	if r.In != "utterances" {
		if chats, err = s.a.cfg.Store.ChatMatchesBefore(ctx, s.sid, r.Query, r.BeforeMs, s.maxSeq, cut.SearchMaxResults); err != nil {
			return nil, nil, err
		}
	}
	// Both lists run oldest to newest; the newest are nearest the moment.
	nUtt := min(len(utts), cut.SearchMaxResults-min(len(chats), searchChatShare))
	nChat := min(len(chats), cut.SearchMaxResults-nUtt)
	out := utteranceEvidence(utts[len(utts)-nUtt:])
	return append(out, chatEvidence(chats[len(chats)-nChat:])...), nil, nil
}

// read returns the utterances (first) and chat of [from, to).
func (s *cutSearcher) read(ctx context.Context, from, to int64) ([]cut.Evidence, []cut.ActivityPoint, error) {
	var out []cut.Evidence
	if s.transcriptID != "" {
		utts, err := s.a.cfg.Store.UtterancesBetween(ctx, s.transcriptID, from, to, cut.ReadMaxUtterances)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, utteranceEvidence(utts)...)
	}
	chats, _, err := s.a.cfg.Store.ChatsBetween(ctx, s.sid, from, to, s.maxSeq, cut.ReadMaxChats, 0)
	if err != nil {
		return nil, nil, err
	}
	return append(out, chatEvidence(chats)...), nil, nil
}

func utteranceEvidence(utts []domain.Utterance) []cut.Evidence {
	out := make([]cut.Evidence, 0, len(utts))
	for _, u := range utts {
		out = append(out, cut.Evidence{Ref: "utt:" + u.ID, Kind: cut.KindUtterance, StartMs: u.StartMs, EndMs: u.EndMs, Speaker: u.Speaker, Text: u.Text})
	}
	return out
}

// chatEvidence is chat as evidence: messages the platform hid (its bot
// filter) are left out, and donations, subscriptions and system notices
// are marked so they are not read as viewers' words.
func chatEvidence(chats []domain.ChatEvent) []cut.Evidence {
	out := make([]cut.Evidence, 0, len(chats))
	for _, c := range chats {
		if c.Hidden {
			continue
		}
		e := cut.Evidence{Ref: "chat:" + c.ID, Kind: cut.KindChat, StartMs: c.OffsetMs, Text: c.Text, ChatKind: c.Kind}
		if c.AuthorKey != "" {
			author := c.AuthorKey
			e.Author = &author
		}
		out = append(out, e)
	}
	return out
}
