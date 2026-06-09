package events

import (
	"testing"
	"time"
)

func TestHub_PublishReachesSubscribers(t *testing.T) {
	h := NewHub()
	ch1, unsub1 := h.Subscribe()
	ch2, unsub2 := h.Subscribe()
	defer unsub1()
	defer unsub2()

	h.Publish(Event{Type: TypeECGIngested, ECGID: "e1"})

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.ECGID != "e1" || ev.Type != TypeECGIngested {
				t.Errorf("sub %d got %+v", i, ev)
			}
			if ev.At == "" {
				t.Errorf("sub %d: At not stamped", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d: no event received", i)
		}
	}
}

func TestHub_UnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, unsub := h.Subscribe()
	unsub()

	// Channel is closed by unsubscribe.
	if _, ok := <-ch; ok {
		t.Error("channel should be closed after unsubscribe")
	}

	// Publishing after unsubscribe must not panic.
	h.Publish(Event{Type: TypeECGQuarantined})
}

func TestHub_SlowSubscriberDoesNotBlock(t *testing.T) {
	h := NewHub()
	_, unsub := h.Subscribe() // never drained
	defer unsub()

	done := make(chan struct{})
	go func() {
		// Far exceeds the 32-slot buffer; Publish must drop, never block.
		for i := 0; i < 1000; i++ {
			h.Publish(Event{Type: TypeECGIngested})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
}
