package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/privacy"
	"github.com/manwol-ryu/momentforge/internal/store"
)

const (
	commentPages        = 100   // CzzEdit's page budget
	commentPasteLimit   = 12000 // characters
	commentParseVersion = 1
)

// CommentRequest asks for a video's viewer recommendations.
type CommentRequest struct {
	SessionID string // an existing VOD session, or
	URL       string // a VOD address (its session is reused or created)
	Text      string // pasted comments instead of reading the website
}

// StartComments reads (or takes pasted) comments in the background and
// turns the times in them into viewer recommendations.
func (a *App) StartComments(ctx context.Context, req CommentRequest) (JobStatus, error) {
	req.Text = strings.TrimSpace(req.Text)
	if utf8.RuneCountInString(req.Text) > commentPasteLimit {
		return JobStatus{}, domain.NewUserError("INPUT", "붙여넣기는 12,000자 이하 텍스트여야 합니다.")
	}
	var sess store.Session
	var meta *platform.VODMeta
	var p platform.Platform
	var target platform.Target
	switch {
	case req.SessionID != "":
		s, err := a.cfg.Store.Session(ctx, req.SessionID)
		if err != nil {
			return JobStatus{}, err
		}
		if s.Kind != string(platform.KindVOD) {
			return JobStatus{}, domain.NewUserError("VOD_ONLY", "영상 댓글은 다시보기에서만 읽을 수 있습니다.")
		}
		sess, target = s, platform.Target{Platform: s.Platform, Kind: platform.KindVOD, ID: s.TargetID}
		p, _ = a.cfg.Platforms.Get(s.Platform)
	case strings.TrimSpace(req.URL) != "":
		var err error
		if p, target, err = a.cfg.Platforms.Resolve(req.URL); err != nil {
			return JobStatus{}, err
		}
		if target.Kind != platform.KindVOD {
			return JobStatus{}, domain.NewUserError("VOD_ONLY", "영상 댓글은 다시보기 주소에서만 읽을 수 있습니다.")
		}
	default:
		return JobStatus{}, domain.NewUserError("INPUT", "다시보기 주소를 입력하거나 저장한 작업을 열어 주세요.")
	}
	if _, ok := p.(platform.CommentSource); !ok && req.Text == "" {
		return JobStatus{}, domain.NewUserError("UNSUPPORTED", "이 영상의 댓글은 자동으로 읽을 수 없습니다. 시각이 적힌 댓글을 붙여넣어 주세요.")
	}
	return a.jobs.start(a.ctx, "comments", func(ctx context.Context, h job) error {
		if sess.ID == "" {
			src, ok := p.(platform.VODSource)
			if !ok {
				return domain.NewUserError("UNSUPPORTED", "이 주소의 영상 정보를 읽을 수 없습니다.")
			}
			h.update(func(s *JobStatus) { s.Phase, s.Message = "checking", "영상 정보를 확인하는 중입니다." })
			var err error
			if sess, err = a.commentSession(ctx, p, src, target, meta); err != nil {
				return err
			}
		}
		h.update(func(s *JobStatus) {
			s.SessionID, s.Title, s.DurationMs = sess.ID, sess.Title, sess.DurationMs
			s.Phase, s.Message = "comments", "영상 댓글을 읽는 중입니다."
		})
		c, recs, err := a.captureComments(ctx, sess, req.Text, h)
		if err != nil {
			return err
		}
		h.update(func(s *JobStatus) {
			s.Recommendations = recs
			s.Message = fmt.Sprintf("시청자 추천 %d개를 찾았습니다.", recs)
			if c.State == "partial" && c.Source == "website" {
				s.Warning = "일부 댓글을 읽지 못했습니다 (" + strings.Join(c.Issues, ", ") + "). 읽은 댓글로 추천을 만들었습니다."
			}
		})
		return nil
	})
}

// commentSession reuses the newest saved session of the video or creates a
// comments-only one (no chat yet; collecting chat later continues it).
func (a *App) commentSession(ctx context.Context, p platform.Platform, src platform.VODSource, t platform.Target, _ *platform.VODMeta) (store.Session, error) {
	list, err := a.cfg.Store.Sessions(ctx, false)
	if err != nil {
		return store.Session{}, err
	}
	for _, s := range list {
		if s.Platform == p.ID() && s.Kind == string(platform.KindVOD) && s.TargetID == t.ID {
			return s, nil
		}
	}
	meta, err := src.VODMeta(ctx, t)
	if err != nil {
		return store.Session{}, err
	}
	salt, err := privacy.NewSalt()
	if err != nil {
		return store.Session{}, err
	}
	s, err := a.cfg.Store.CreateSession(ctx, store.NewSession{Platform: p.ID(), Kind: string(platform.KindVOD),
		TargetID: t.ID, Title: meta.Title, DurationMs: meta.DurationMs, Salt: salt})
	if err != nil {
		return store.Session{}, err
	}
	if err := a.cfg.Store.SetState(ctx, s.ID, store.StateCommentsOnly, "", ""); err != nil {
		return store.Session{}, err
	}
	return a.cfg.Store.Session(ctx, s.ID)
}

// captureComments reads the website's comments (or takes pasted text),
// pseudonymizes authors, parses times and saves everything with its files.
func (a *App) captureComments(ctx context.Context, sess store.Session, text string, h job) (store.CommentCapture, int, error) {
	salt, err := a.cfg.Store.SessionSalt(ctx, sess.ID)
	if err != nil {
		return store.CommentCapture{}, 0, err
	}
	capture := store.CommentCapture{SessionID: sess.ID, ParserVersion: commentParseVersion, ReportedCount: -1}
	var raw []platform.Comment
	if text != "" {
		raw = []platform.Comment{{ID: digest(text)[:24], Text: text}}
		capture.Source, capture.State, capture.Issues = "manual_paste", "partial", []string{"manual_paste"}
	} else {
		p, _ := a.cfg.Platforms.Get(sess.Platform)
		src, ok := p.(platform.CommentSource)
		if !ok {
			return capture, 0, domain.NewUserError("UNSUPPORTED", "이 영상의 댓글은 자동으로 읽을 수 없습니다. 시각이 적힌 댓글을 붙여넣어 주세요.")
		}
		res, err := src.VODComments(ctx, platform.Target{Platform: sess.Platform, Kind: platform.KindVOD, ID: sess.TargetID}, commentPages,
			func(pages, n int) {
				// Pages stays the chat page count; comment pages are only in the message.
				h.update(func(s *JobStatus) {
					s.Comments, s.Message = n, fmt.Sprintf("영상 댓글을 읽는 중입니다 (%d쪽, 댓글 %d개).", pages, n)
				})
			})
		if err != nil {
			return capture, 0, err
		}
		raw, capture.Source, capture.Pages, capture.ReportedCount, capture.Issues = res.Comments, "website", res.Pages, res.ReportedCount, res.Issues
		capture.State = "finished"
		if len(res.Issues) > 0 {
			capture.State = "partial"
		}
	}

	var comments []store.StoredComment
	var recs []store.ViewerRecommendation
	for _, c := range raw {
		sc := store.StoredComment{CommentID: c.ID, ParentID: c.ParentID, AuthorKey: privacy.AuthorKey(salt, c.AuthorID), Content: c.Text}
		var found []TimeRef
		var issues []TimeIssue
		if c.Hidden {
			sc.Content, sc.Status = "", "unavailable"
		} else {
			found, issues = ParseTimestamps(c.Text, sess.DurationMs)
			switch {
			case len(found) > 0:
				sc.Status = "parsed"
			case len(issues) > 0:
				sc.Status = "needs_review"
			default:
				sc.Status = "no_timestamp"
			}
		}
		sc.IssuesJSON = issuesJSON(issues)
		comments = append(comments, sc)
		for _, f := range found {
			recs = append(recs, store.ViewerRecommendation{
				ID:        digest([]any{sess.TargetID, c.ID, c.Text, f.StartMs, f.EndMs})[:32],
				CommentID: c.ID, StartMs: f.StartMs, EndMs: f.EndMs, Description: f.Description})
		}
	}
	saved, err := a.cfg.Store.SaveCommentCapture(context.WithoutCancel(ctx), capture, comments, recs)
	if err != nil {
		return capture, 0, err
	}
	if err := a.writeComments(saved, sess, comments, recs); err != nil {
		a.logError("write comments", err)
	}
	a.writeUnified(context.WithoutCancel(ctx), sess.ID)
	return saved, len(recs), nil
}

func issuesJSON(issues []TimeIssue) []byte {
	type row struct {
		Offset int    `json:"offset"`
		Reason string `json:"reason"`
	}
	out := make([]row, len(issues))
	for i, is := range issues {
		out[i] = row{is.Offset, is.Reason}
	}
	b, _ := json.Marshal(out)
	return b
}

// digest is a short stable ID for derived records.
func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *App) writeComments(c store.CommentCapture, sess store.Session, comments []store.StoredComment, recs []store.ViewerRecommendation) error {
	m := export.CommentManifest{CaptureID: c.ID, SessionID: sess.ID, VideoID: sess.TargetID, Source: c.Source, State: c.State,
		Pages: c.Pages, Issues: c.Issues, ParserVersion: c.ParserVersion}
	if c.ReportedCount >= 0 {
		m.ReportedCount = &c.ReportedCount
	}
	rows := make([]export.CommentRow, len(comments))
	for i, cm := range comments {
		rows[i] = export.CommentRow{CommentID: cm.CommentID, ParentID: optional(cm.ParentID), AuthorKey: optional(cm.AuthorKey),
			Content: cm.Content, Status: cm.Status, Issues: cm.IssuesJSON}
	}
	out := make([]export.RecommendationRow, len(recs))
	for i, r := range recs {
		out[i] = export.RecommendationRow{ID: r.ID, CommentID: r.CommentID, StartMs: r.StartMs, Description: r.Description,
			SourceType: "viewer_comment", ReviewState: "미검토", Source: c.Source, ParseKind: "text_timestamp"}
		if r.EndMs > 0 {
			out[i].EndMs = &recs[i].EndMs
		}
	}
	return export.WriteComments(filepath.Join(a.exportDir(sess.ID), "comments", c.ID), m, rows, out)
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// CommentsView is a session's newest comment capture.
type CommentsView struct {
	CaptureID       string
	Source          string // website | manual_paste
	State           string // finished | partial
	Pages           int
	ReportedCount   int // -1 unknown
	CommentCount    int
	Issues          []string
	CreatedAt       time.Time
	Recommendations []ViewerRecView
	NeedsReview     []CommentLine // comments with times that were not used (first 50)
}

// ViewerRecView is a recommendation with its comment.
type ViewerRecView struct {
	ID          string
	CommentID   string
	StartMs     int64
	EndMs       int64 // 0 single time
	Description string
	CommentText string
	WatchURL    string
}

// CommentLine is a comment that needs a person to read it.
type CommentLine struct {
	CommentID string
	Text      string
	Reasons   []string
}

// Comments returns the newest capture of a session, or nil when there is none.
func (a *App) Comments(ctx context.Context, sid string) (*CommentsView, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return nil, err
	}
	c, ok, err := a.cfg.Store.LatestCommentCapture(ctx, sid)
	if err != nil || !ok {
		return nil, err
	}
	comments, err := a.cfg.Store.CaptureComments(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	recs, err := a.cfg.Store.ViewerRecommendations(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	v := &CommentsView{CaptureID: c.ID, Source: c.Source, State: c.State, Pages: c.Pages, ReportedCount: c.ReportedCount,
		CommentCount: len(comments), Issues: c.Issues, CreatedAt: c.CreatedAt, Recommendations: []ViewerRecView{}, NeedsReview: []CommentLine{}}
	text := make(map[string]string, len(comments))
	for _, cm := range comments {
		text[cm.CommentID] = cm.Content
		if cm.Status == "needs_review" && len(v.NeedsReview) < 50 {
			var issues []struct {
				Reason string `json:"reason"`
			}
			json.Unmarshal(cm.IssuesJSON, &issues)
			line := CommentLine{CommentID: cm.CommentID, Text: cm.Content}
			for _, is := range issues {
				line.Reasons = append(line.Reasons, is.Reason)
			}
			v.NeedsReview = append(v.NeedsReview, line)
		}
	}
	link := a.linker(s)
	for _, r := range recs {
		rv := ViewerRecView{ID: r.ID, CommentID: r.CommentID, StartMs: r.StartMs, EndMs: r.EndMs, Description: r.Description, CommentText: text[r.CommentID]}
		if link != nil {
			rv.WatchURL = link(r.StartMs)
		}
		v.Recommendations = append(v.Recommendations, rv)
	}
	return v, nil
}
