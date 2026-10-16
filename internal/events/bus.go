// Package events is an in-process publish/subscribe bus. Jobs publish
// progress here and the API streams it to the browser over SSE.
package events

import (
	"sync"
	"time"
)

// Event is one notification. Data must be JSON-serializable.
type Event struct {
	Type string    `json:"type"`
	At   time.Time `json:"at"`
	Data any       `json:"data,omitempty"`
}

// Bus fans events out to subscribers. Each subscriber has a bounded buffer;
// a subscriber that falls behind is closed and marked overflowed instead of
// silently losing events, so the client can reconnect and reload state.
type Bus struct {
	mu     sync.Mutex
	subs   map[*Subscription]struct{}
	buffer int
	closed bool
}

// NewBus creates a bus whose subscribers buffer up to buffer events.
func NewBus(buffer int) *Bus {
	if buffer < 1 {
		buffer = 1
	}
	return &Bus{subs: make(map[*Subscription]struct{}), buffer: buffer}
}

// Subscription receives events until Done is closed.
type Subscription struct {
	bus        *Bus
	ch         chan Event
	done       chan struct{}
	once       sync.Once
	overflowed bool
}

// Subscribe registers a new subscriber. On a closed bus the subscription is already done.
func (b *Bus) Subscribe() *Subscription {
	s := &Subscription{bus: b, ch: make(chan Event, b.buffer), done: make(chan struct{})}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(s.done)
		return s
	}
	b.subs[s] = struct{}{}
	return s
}

// Publish delivers e to every subscriber without blocking.
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		select {
		case s.ch <- e:
		default:
			s.overflowed = true
			b.removeLocked(s)
		}
	}
}

// Close ends every subscription. Later Subscribe calls return done subscriptions.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for s := range b.subs {
		b.removeLocked(s)
	}
}

func (b *Bus) removeLocked(s *Subscription) {
	delete(b.subs, s)
	s.once.Do(func() { close(s.done) })
}

// C returns the event channel. Events already buffered stay readable after Done.
func (s *Subscription) C() <-chan Event { return s.ch }

// Done is closed when the subscription ends (Close, bus Close, or overflow).
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Overflowed reports whether the subscription ended because it fell behind.
func (s *Subscription) Overflowed() bool {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	return s.overflowed
}

// Close unsubscribes.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	s.bus.removeLocked(s)
}
