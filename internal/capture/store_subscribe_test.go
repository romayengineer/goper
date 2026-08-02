package capture

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSubscribe(t *testing.T) {
	rb := NewRingBuffer(10)
	ch := rb.Subscribe()
	defer rb.Unsubscribe(ch)

	rb.Push(newTestEntry("a"))

	select {
	case got := <-ch:
		assert.Equal(t, EntryID("a"), got.ID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscribed entry")
	}
}

func TestSubscribeDoesNotBlock(t *testing.T) {
	rb := NewRingBuffer(10)
	ch := rb.Subscribe()
	defer rb.Unsubscribe(ch)

	for i := 0; i < 500; i++ {
		rb.Push(newTestEntry(string(rune('a' + i%26))))
	}
}

func TestUnsubscribe(t *testing.T) {
	rb := NewRingBuffer(10)
	ch := rb.Subscribe()

	rb.Unsubscribe(ch)

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "expected channel to be closed after unsubscribe")
	case <-time.After(time.Second):
		t.Fatal("expected channel close after unsubscribe")
	}
}

func TestSubscribeMultiple(t *testing.T) {
	rb := NewRingBuffer(10)
	ch1 := rb.Subscribe()
	ch2 := rb.Subscribe()
	defer rb.Unsubscribe(ch1)
	defer rb.Unsubscribe(ch2)

	rb.Push(newTestEntry("a"))

	recvEntry(t, ch1)
	recvEntry(t, ch2)
}

// recvEntry receives one entry from ch, failing the test on timeout.
func recvEntry(t *testing.T, ch <-chan *CapturedEntry) *CapturedEntry {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for entry")
		return nil
	}
}

func TestSubscribersSurviveClear(t *testing.T) {
	rb := NewRingBuffer(10)
	ch := rb.Subscribe()
	defer rb.Unsubscribe(ch)

	rb.Push(newTestEntry("a"))
	awaitSubscriberEvent(t, ch, EntryID("a"), "pre-clear event")

	rb.Clear()
	rb.Push(newTestEntry("b"))
	awaitSubscriberEvent(t, ch, EntryID("b"), "subscribers should keep receiving events after Clear")
}

// awaitSubscriberEvent waits for one event on ch and asserts it matches want,
// failing the test on timeout.
func awaitSubscriberEvent(t *testing.T, ch <-chan *CapturedEntry, want EntryID, msg string) {
	t.Helper()
	select {
	case e := <-ch:
		assert.Equal(t, want, e.ID, msg)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for " + msg)
	}
}
