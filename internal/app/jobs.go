package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/events"
)

// JobStatus is the visible state of the current (or last) background job.
// It is published on the event bus as "job" every time it changes.
type JobStatus struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // "collect" | "analyze"
	Busy       bool      `json:"busy"`
	Phase      string    `json:"phase"` // checking, collecting, analyzing, done, paused, error
	Message    string    `json:"message"`
	Code       string    `json:"code,omitempty"`
	SessionID  string    `json:"sessionId,omitempty"`
	Title      string    `json:"title,omitempty"`
	PositionMs int64     `json:"positionMs"`
	DurationMs int64     `json:"durationMs"`
	Pages      int       `json:"pages"`
	AnalysisID string    `json:"analysisId,omitempty"`
	Candidates int       `json:"candidates"`
	Warning    string    `json:"warning,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type jobs struct {
	mu     sync.Mutex
	status JobStatus
	cancel context.CancelFunc
	done   chan struct{}
	bus    *events.Bus
}

// job is the handle a running job uses to report progress.
type job struct {
	j *jobs
}

func (h job) update(f func(s *JobStatus)) {
	h.j.mu.Lock()
	f(&h.j.status)
	h.j.status.UpdatedAt = time.Now().UTC()
	s := h.j.status
	h.j.mu.Unlock()
	if h.j.bus != nil {
		h.j.bus.Publish(events.Event{Type: "job", Data: s})
	}
}

var errBusy = domain.NewUserError("BUSY", "진행 중인 작업을 먼저 마쳐 주세요.")

// start runs fn in the background unless another job is busy.
func (j *jobs) start(parent context.Context, kind string, fn func(ctx context.Context, h job) error) (JobStatus, error) {
	j.mu.Lock()
	if j.status.Busy {
		j.mu.Unlock()
		return JobStatus{}, errBusy
	}
	ctx, cancel := context.WithCancel(parent)
	j.cancel = cancel
	j.done = make(chan struct{})
	j.status = JobStatus{ID: newJobID(), Kind: kind, Busy: true, Phase: "starting", Message: "작업을 시작합니다.", UpdatedAt: time.Now().UTC()}
	s, done := j.status, j.done
	j.mu.Unlock()

	h := job{j}
	h.update(func(*JobStatus) {})
	go func() {
		defer close(done)
		defer cancel()
		err := fn(ctx, h)
		h.update(func(s *JobStatus) {
			s.Busy = false
			switch {
			case err == nil:
				s.Phase, s.Code = "done", ""
			case errors.Is(err, context.Canceled):
				s.Phase, s.Code, s.Message = "paused", "CANCELLED", "작업을 중단했습니다. 저장된 위치에서 이어받을 수 있습니다."
			default:
				s.Phase = "error"
				if ue, ok := domain.AsUserError(err); ok {
					s.Code, s.Message = ue.Code, ue.Message
				} else {
					slog.Error("job failed", "kind", kind, "err", err)
					s.Code, s.Message = "UNEXPECTED", "작업을 완료하지 못했습니다. 저장된 결과를 확인하고 다시 시도해 주세요."
				}
			}
		})
	}()
	return s, nil
}

func (j *jobs) current() JobStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

// stop cancels the running job and waits up to ctx for it to finish.
func (j *jobs) stop(ctx context.Context) {
	j.mu.Lock()
	cancel, done := j.cancel, j.done
	j.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func newJobID() string { return "job-" + time.Now().UTC().Format("20060102T150405.000") }
