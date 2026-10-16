package events

import "testing"

func TestPublishReachesSubscribers(t *testing.T) {
	b := NewBus(4)
	a, c := b.Subscribe(), b.Subscribe()
	b.Publish(Event{Type: "x"})
	for _, s := range []*Subscription{a, c} {
		if got := <-s.C(); got.Type != "x" || got.At.IsZero() {
			t.Fatalf("got %+v", got)
		}
	}
}

func TestSlowSubscriberIsClosedNotSilentlyDropped(t *testing.T) {
	b := NewBus(2)
	slow := b.Subscribe()
	for i := 0; i < 3; i++ {
		b.Publish(Event{Type: "tick"})
	}
	select {
	case <-slow.Done():
	default:
		t.Fatal("overflowing subscriber should be done")
	}
	if !slow.Overflowed() {
		t.Fatal("Overflowed() = false, want true")
	}
	if n := len(slow.C()); n != 2 {
		t.Fatalf("buffered events = %d, want 2 still readable", n)
	}
}

func TestCloseEndsSubscriptions(t *testing.T) {
	b := NewBus(1)
	s := b.Subscribe()
	b.Close()
	<-s.Done()
	if s.Overflowed() {
		t.Fatal("closed by bus, not overflow")
	}
	late := b.Subscribe()
	<-late.Done()
	s.Close() // second close is safe
}
