package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/manwol-ryu/momentforge/internal/events"
)

// events streams bus events as Server-Sent Events. The client reads it with
// fetch (so it can send the access key header) and reconnects on close.
func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Bus == nil {
		writeError(w, http.StatusServiceUnavailable, "NO_EVENTS", "실시간 알림을 사용할 수 없습니다.")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // the stream outlives normal write timeouts

	sub := h.cfg.Bus.Subscribe()
	defer sub.Close()

	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream; charset=utf-8")
	hd.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(e events.Event) bool {
		data, err := json.Marshal(e)
		if err != nil {
			data = []byte(`{}`)
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send(events.Event{Type: "ready", At: time.Now().UTC()}) {
		return
	}

	tick := time.NewTicker(h.cfg.Heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-sub.C():
			if !send(e) {
				return
			}
		case <-sub.Done():
			for len(sub.C()) > 0 {
				if !send(<-sub.C()) {
					return
				}
			}
			if sub.Overflowed() {
				// The client fell behind: tell it to reload state, then reconnect.
				send(events.Event{Type: "overflow", At: time.Now().UTC()})
			}
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
