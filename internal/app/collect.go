package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/privacy"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// CollectRequest starts or resumes a VOD collection.
type CollectRequest struct {
	URL       string
	Sensitive bool
	Resume    string // session ID to continue; empty starts a new session
	// IncludeComments also reads the video's comments for viewer
	// recommendations after the chat (CzzEdit's default).
	IncludeComments bool
}

// StartCollect validates the URL now and collects in the background.
func (a *App) StartCollect(req CollectRequest) (JobStatus, error) {
	p, target, err := a.cfg.Platforms.Resolve(req.URL)
	if err != nil {
		return JobStatus{}, err
	}
	if target.Kind == platform.KindLive {
		return JobStatus{}, domain.NewUserError("LIVE_URL", "생방송 주소입니다. 생방송 수집 칸에서 시작하세요.")
	}
	src, ok := p.(platform.VODSource)
	if !ok || target.Kind != platform.KindVOD {
		return JobStatus{}, domain.NewUserError("UNSUPPORTED", "이 주소는 아직 수집할 수 없습니다. 다시보기 주소를 입력해 주세요.")
	}
	return a.startCollect(p, src, target, req.Resume, a.params(req.Sensitive), req.IncludeComments)
}

// ResumeSession continues a paused collection from its saved position.
func (a *App) ResumeSession(ctx context.Context, sid string, sensitive bool) (JobStatus, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return JobStatus{}, err
	}
	p, ok := a.cfg.Platforms.Get(s.Platform)
	src, isVOD := p.(platform.VODSource)
	if !ok || !isVOD || s.Kind != string(platform.KindVOD) {
		return JobStatus{}, domain.NewUserError("UNSUPPORTED", "이 작업은 이어받을 수 없습니다.")
	}
	target := platform.Target{Platform: s.Platform, Kind: platform.KindVOD, ID: s.TargetID}
	_, hasComments, err := a.cfg.Store.LatestCommentCapture(ctx, sid)
	if err != nil {
		return JobStatus{}, err
	}
	return a.startCollect(p, src, target, sid, a.params(sensitive), !hasComments)
}

func (a *App) startCollect(p platform.Platform, src platform.VODSource, target platform.Target, resume string, params detect.Params, comments bool) (JobStatus, error) {
	return a.jobs.start(a.ctx, "collect", func(ctx context.Context, h job) error {
		return a.collect(ctx, h, p, src, target, resume, params, comments)
	})
}

func (a *App) collect(ctx context.Context, h job, p platform.Platform, src platform.VODSource,
	target platform.Target, resume string, params detect.Params, comments bool) (err error) {
	h.update(func(s *JobStatus) { s.Phase, s.Message = "checking", "영상 정보를 확인하는 중입니다." })
	meta, err := src.VODMeta(ctx, target)
	if err != nil {
		return err
	}

	st := a.cfg.Store
	var sess store.Session
	var salt []byte
	if resume != "" {
		if sess, err = st.Session(ctx, resume); err != nil {
			return err
		}
		if sess.Platform != p.ID() || sess.TargetID != target.ID || sess.ReportedDurationMs != meta.DurationMs {
			return domain.NewUserError("SOURCE_CHANGED", "저장 당시와 영상 정보가 다릅니다. 새 작업으로 수집해 주세요.")
		}
		if salt, err = st.SessionSalt(ctx, sess.ID); err != nil {
			return err
		}
		if sess.State != store.StateFinished {
			if err := st.SetState(ctx, sess.ID, store.StateCollecting, "", ""); err != nil {
				return err
			}
		}
	} else {
		if salt, err = privacy.NewSalt(); err != nil {
			return err
		}
		sess, err = st.CreateSession(ctx, store.NewSession{Platform: p.ID(), Kind: string(platform.KindVOD),
			TargetID: target.ID, Title: meta.Title, DurationMs: meta.DurationMs, Salt: salt})
		if err != nil {
			return err
		}
	}
	h.update(func(s *JobStatus) {
		s.SessionID, s.Title, s.DurationMs, s.PositionMs, s.Pages = sess.ID, sess.Title, meta.DurationMs, sess.Cursor, sess.Pages
		s.Phase, s.Message = "collecting", "채팅을 받는 중입니다."
	})

	// A failure while collecting pauses the session and keeps what was saved.
	collecting := sess.State != store.StateFinished
	defer func() {
		if err != nil && collecting {
			a.pause(sess.ID, err)
			a.writeChat(context.WithoutCancel(ctx), sess.ID)
		}
	}()

	dir := a.exportDir(sess.ID)
	warning := ""
	if received, rerr := st.Chats(ctx, sess.ID, store.AllSeq, true); rerr != nil || export.WriteReceived(dir, received) != nil {
		warning = "수신 TXT 갱신에 실패했습니다. DB 저장은 계속합니다."
	}
	cursor, pages, finished := sess.Cursor, sess.Pages, sess.State == store.StateFinished
	for !finished {
		page, err := src.VODChats(ctx, target, cursor, meta.DurationMs)
		if err != nil {
			var pe *platform.PageError
			if errors.As(err, &pe) {
				st.Quarantine(context.WithoutCancel(ctx), sess.ID, cursor, pe.Raw)
			}
			return err
		}
		evs, maxEnd := toEvents(salt, page.Messages)
		next := page.Next
		if page.Finished {
			next = max(meta.DurationMs, maxEnd)
		}
		added, err := st.SavePage(ctx, sess.ID, store.Page{Cursor: cursor, Raw: page.Raw, Events: evs,
			Next: next, Finished: page.Finished, Gaps: page.Gaps})
		if err != nil {
			return err
		}
		if export.AppendReceived(dir, added) != nil {
			warning = "수신 TXT 갱신에 실패했습니다. DB 원본은 저장되어 있습니다."
		}
		cursor, finished = next, page.Finished
		pages++
		h.update(func(s *JobStatus) { s.PositionMs, s.Pages, s.Warning = cursor, pages, warning })
	}
	collecting = false

	// The chat is read once for its files and the analysis.
	chat, err := a.loadChat(ctx, sess.ID)
	if err != nil {
		return err
	}
	if err := writeChatFiles(dir, chat); err != nil {
		warning = "채팅 파일을 쓰지 못했습니다. 저장된 채팅으로 다시 분석할 수 있습니다."
	}
	h.update(func(s *JobStatus) {
		s.Phase, s.Message, s.Warning = "analyzing", "편집 후보를 찾는 중입니다.", warning
	})
	an, err := a.analyzeChat(ctx, chat, params, h)
	if err != nil {
		return err
	}
	h.update(func(s *JobStatus) { s.AnalysisID, s.Candidates = an.ID, len(an.Candidates) })
	if _, ok := p.(platform.CommentSource); comments && ok {
		// Comments are extra: a failure keeps the chat and says why.
		h.update(func(s *JobStatus) { s.Phase, s.Message = "comments", "영상 댓글을 읽는 중입니다." })
		if _, n, cerr := a.captureComments(ctx, sess, "", h); cerr != nil {
			msg := "읽지 못했습니다"
			if ue, ok := domain.AsUserError(cerr); ok {
				msg = ue.Message
			}
			warning = "채팅 저장 완료. 영상 댓글: " + msg
		} else {
			h.update(func(s *JobStatus) { s.Recommendations = n })
		}
	}
	h.update(func(s *JobStatus) { s.Message, s.Warning = "저장과 분석이 끝났습니다.", warning })
	// Then what 일반 설정 turned on: the broadcast's transcript, its AI fix and the scenes' explanations.
	return a.runAfter(ctx, h, sess.ID, a.autoStepsFor(), "저장과 분석이 끝났습니다.")
}

// toEvents pseudonymizes a page. Identical (offset, author, text) messages in
// one page are genuine repeats and get distinct IDs by occurrence.
func toEvents(salt []byte, msgs []platform.RawChat) ([]domain.ChatEvent, int64) {
	type key struct {
		t      int64
		author string
		text   string
	}
	seen := map[key]int{}
	out := make([]domain.ChatEvent, 0, len(msgs))
	maxEnd := int64(0)
	for _, m := range msgs {
		k := key{m.OffsetMs, m.AuthorID, m.Text}
		seen[k]++
		out = append(out, domain.ChatEvent{
			ID:        privacy.EventID(salt, m.OffsetMs, m.AuthorID, m.Text, seen[k]),
			OffsetMs:  m.OffsetMs,
			AuthorKey: privacy.AuthorKey(salt, m.AuthorID),
			Text:      m.Text,
		})
		maxEnd = max(maxEnd, m.OffsetMs+1)
	}
	return out, maxEnd
}

// pause records why collection stopped. It must work after cancellation.
func (a *App) pause(sid string, cause error) {
	code, msg := "UNEXPECTED", "수집이 중단되었습니다. 저장된 위치에서 이어받을 수 있습니다."
	if errors.Is(cause, context.Canceled) {
		code, msg = "CANCELLED", "수집을 중단했습니다. 저장된 위치에서 이어받을 수 있습니다."
	} else if ue, ok := domain.AsUserError(cause); ok {
		code, msg = ue.Code, ue.Message
	}
	if err := a.cfg.Store.SetState(context.Background(), sid, store.StatePaused, code, msg); err != nil {
		a.logError("pause session", fmt.Errorf("%s: %w", sid, err))
	}
}
