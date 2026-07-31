package capture

import (
	"sync"
	"testing"
	"time"
)

func newTestEntry(id string) *CapturedEntry {
	return &CapturedEntry{
		ID:         EntryID(id),
		Method:     "GET",
		URL:        "http://example.com/" + id,
		StatusCode: 200,
	}
}

func TestPushGet(t *testing.T) {
	rb := NewRingBuffer(10)
	entry := newTestEntry("a")

	rb.Push(entry)

	got := rb.Get("a")
	if got == nil {
		t.Fatal("expected entry to be found")
	}
	if got.ID != "a" {
		t.Fatalf("got id %q, want %q", got.ID, "a")
	}
}

func TestGetMissing(t *testing.T) {
	rb := NewRingBuffer(10)
	if got := rb.Get("nope"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestPushAssignsID(t *testing.T) {
	rb := NewRingBuffer(10)
	entry := &CapturedEntry{Method: "POST"}

	rb.Push(entry)

	if entry.ID == "" {
		t.Fatal("expected Push to assign an ID")
	}
	if got := rb.Get(entry.ID); got == nil {
		t.Fatalf("expected entry with auto-assigned id %q to be found", entry.ID)
	}
}

func TestPushEviction(t *testing.T) {
	rb := NewRingBuffer(3)

	for i := 0; i < 4; i++ {
		rb.Push(newTestEntry(string(rune('a' + i))))
	}

	if rb.Len() != 3 {
		t.Fatalf("expected len 3 after eviction, got %d", rb.Len())
	}
	if got := rb.Get(EntryID("a")); got != nil {
		t.Fatal("expected oldest entry 'a' to be evicted")
	}
	for _, id := range []string{"b", "c", "d"} {
		if got := rb.Get(EntryID(id)); got == nil {
			t.Fatalf("expected entry %q to survive", id)
		}
	}
}

func TestListAll(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))
	rb.Push(newTestEntry("c"))

	entries := rb.List(ListOpts{})
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestListSince(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("old"))
	rb.Get("old").Timestamp = time.Now().Add(-time.Hour)

	rb.Push(newTestEntry("new"))

	entries := rb.List(ListOpts{Since: time.Now().Add(-time.Minute)})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after since filter, got %d", len(entries))
	}
	if entries[0].ID != "new" {
		t.Fatalf("expected 'new' to pass since filter, got %q", entries[0].ID)
	}
}

func TestListMethod(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("get"))
	post := newTestEntry("post")
	post.Method = "POST"
	rb.Push(post)

	entries := rb.List(ListOpts{Method: "POST"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 POST entry, got %d", len(entries))
	}
	if entries[0].ID != "post" {
		t.Fatalf("expected 'post', got %q", entries[0].ID)
	}
}

func TestListStatus(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("ok"))
	notFound := newTestEntry("nf")
	notFound.StatusCode = 404
	rb.Push(notFound)

	entries := rb.List(ListOpts{Status: 404})
	if len(entries) != 1 || entries[0].ID != "nf" {
		t.Fatalf("expected only 'nf' with status 404, got %d entries", len(entries))
	}
}

func TestListURL(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))

	entries := rb.List(ListOpts{URL: "http://example.com/a"})
	if len(entries) != 1 || entries[0].ID != "a" {
		t.Fatalf("expected only 'a' with matching URL, got %d entries", len(entries))
	}
}

func TestListPagination(t *testing.T) {
	rb := NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		rb.Push(newTestEntry(string(rune('a' + i))))
	}

	entries := rb.List(ListOpts{Limit: 2})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with limit, got %d", len(entries))
	}

	entries = rb.List(ListOpts{Limit: 2, Offset: 2})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with offset 2, got %d", len(entries))
	}

	entries = rb.List(ListOpts{Limit: 10})
	if len(entries) != 5 {
		t.Fatalf("expected all 5 with large limit, got %d", len(entries))
	}
}

func TestListEmpty(t *testing.T) {
	rb := NewRingBuffer(10)
	entries := rb.List(ListOpts{})
	if entries != nil {
		t.Fatalf("expected nil for empty buffer, got %v", entries)
	}
}

func TestClear(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))

	rb.Clear()

	if rb.Len() != 0 {
		t.Fatalf("expected len 0 after clear, got %d", rb.Len())
	}
	if got := rb.Get("a"); got != nil {
		t.Fatal("expected entry to be gone after clear")
	}
}

func TestSubscribe(t *testing.T) {
	rb := NewRingBuffer(10)
	ch := rb.Subscribe()
	defer rb.Unsubscribe(ch)

	entry := newTestEntry("a")
	rb.Push(entry)

	select {
	case got := <-ch:
		if got.ID != "a" {
			t.Fatalf("expected id 'a' on channel, got %q", got.ID)
		}
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
		if ok {
			t.Fatal("expected channel to be closed after unsubscribe")
		}
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

	for name, ch := range map[string]chan *CapturedEntry{"ch1": ch1, "ch2": ch2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("%s did not receive entry", name)
		}
	}
}

func TestConcurrentPushList(t *testing.T) {
	rb := NewRingBuffer(1000)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			rb.Push(newTestEntry(string(rune('a' + i%26))))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = rb.List(ListOpts{Limit: 10})
			_ = rb.Len()
		}
	}()

	wg.Wait()

	if rb.Len() > 1000 {
		t.Fatalf("buffer exceeds capacity: %d", rb.Len())
	}
}
