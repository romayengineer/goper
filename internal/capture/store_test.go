package capture

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NotNil(t, got, "expected entry to be found")
	assert.Equal(t, EntryID("a"), got.ID)
}

func TestGetMissing(t *testing.T) {
	rb := NewRingBuffer(10)
	assert.Nil(t, rb.Get("nope"))
}

func TestPushAssignsID(t *testing.T) {
	rb := NewRingBuffer(10)
	entry := &CapturedEntry{Method: "POST"}

	rb.Push(entry)

	assert.NotEmpty(t, entry.ID, "expected Push to assign an ID")
	assert.NotNil(t, rb.Get(entry.ID), "expected entry with auto-assigned id %q to be found", entry.ID)
}

func TestPushEviction(t *testing.T) {
	rb := NewRingBuffer(3)

	for i := 0; i < 4; i++ {
		rb.Push(newTestEntry(string(rune('a' + i))))
	}

	assert.Equal(t, 3, rb.Len())
	assert.Nil(t, rb.Get(EntryID("a")), "expected oldest entry 'a' to be evicted")
	for _, id := range []string{"b", "c", "d"} {
		assert.NotNil(t, rb.Get(EntryID(id)), "expected entry %q to survive", id)
	}
}

func TestListAll(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))
	rb.Push(newTestEntry("c"))

	entries := rb.List(ListOpts{})
	assert.Len(t, entries, 3)
}

func TestListSince(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("old"))
	rb.Get("old").Timestamp = time.Now().Add(-time.Hour)

	rb.Push(newTestEntry("new"))

	entries := rb.List(ListOpts{Since: time.Now().Add(-time.Minute)})
	require.Len(t, entries, 1)
	assert.Equal(t, EntryID("new"), entries[0].ID)
}

func TestListMethod(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("get"))
	post := newTestEntry("post")
	post.Method = "POST"
	rb.Push(post)

	entries := rb.List(ListOpts{Method: "POST"})
	require.Len(t, entries, 1)
	assert.Equal(t, EntryID("post"), entries[0].ID)
}

func TestListStatus(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("ok"))
	notFound := newTestEntry("nf")
	notFound.StatusCode = 404
	rb.Push(notFound)

	entries := rb.List(ListOpts{Status: 404})
	require.Len(t, entries, 1)
	assert.Equal(t, EntryID("nf"), entries[0].ID)
}

func TestListURL(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))

	entries := rb.List(ListOpts{URL: "http://example.com/a"})
	require.Len(t, entries, 1)
	assert.Equal(t, EntryID("a"), entries[0].ID)
}

func TestListPagination(t *testing.T) {
	rb := NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		rb.Push(newTestEntry(string(rune('a' + i))))
	}

	assert.Len(t, rb.List(ListOpts{Limit: 2}), 2)
	assert.Len(t, rb.List(ListOpts{Limit: 2, Offset: 2}), 2)
	assert.Len(t, rb.List(ListOpts{Limit: 10}), 5)
}

func TestListEmpty(t *testing.T) {
	rb := NewRingBuffer(10)
	assert.Nil(t, rb.List(ListOpts{}))
}

func TestClear(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))

	rb.Clear()

	assert.Equal(t, 0, rb.Len())
	assert.Nil(t, rb.Get("a"), "expected entry to be gone after clear")
}

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

	for name, ch := range map[string]chan *CapturedEntry{"ch1": ch1, "ch2": ch2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("%s did not receive entry", name)
		}
	}
}

func TestNewRingBufferZeroCapacity(t *testing.T) {
	rb := NewRingBuffer(0)
	// capacity <= 0 defaults to 10000
	rb.Push(newTestEntry("a"))
	assert.Equal(t, 1, rb.Len())
	assert.NotNil(t, rb.Get(EntryID("a")))
}

func TestNewRingBufferNegativeCapacity(t *testing.T) {
	rb := NewRingBuffer(-5)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))
	assert.Equal(t, 2, rb.Len())
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

	assert.LessOrEqual(t, rb.Len(), 1000, "buffer exceeds capacity")
}

func TestStatsCountsAndStartTime(t *testing.T) {
	rb := NewRingBuffer(10)
	before := time.Now()
	stats := rb.Stats()
	assert.False(t, before.Sub(stats.StartTime) > time.Second, "start time should be ~now")
	assert.Equal(t, 0, stats.Count)
	assert.Equal(t, 10, stats.Capacity)
	assert.Zero(t, stats.Evictions)
	assert.Zero(t, stats.BytesCaptured)

	rb.Push(newTestEntry("a"))
	stats = rb.Stats()
	assert.Equal(t, 1, stats.Count)
	assert.Zero(t, stats.Evictions)
}

func TestStatsTracksBytesCaptured(t *testing.T) {
	rb := NewRingBuffer(10)
	e := newTestEntry("a")
	req := "request-body"
	resp := "response-body"
	e.RequestBody = &req
	e.ResponseBody = &resp
	rb.Push(e)

	stats := rb.Stats()
	assert.Equal(t, int64(len(req)+len(resp)), stats.BytesCaptured)

	// Bodies that were skipped (nil) contribute nothing.
	e2 := newTestEntry("b")
	rb.Push(e2)
	stats = rb.Stats()
	assert.Equal(t, int64(len(req)+len(resp)), stats.BytesCaptured)
}

func TestStatsTracksEvictions(t *testing.T) {
	rb := NewRingBuffer(2)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))
	assert.Zero(t, rb.Stats().Evictions)

	rb.Push(newTestEntry("c"))
	stats := rb.Stats()
	assert.Equal(t, int64(1), stats.Evictions, "third push should evict the oldest")
	assert.Equal(t, 2, stats.Count)

	// Clear resets entries but keeps lifetime eviction/uptime stats.
	rb.Clear()
	stats = rb.Stats()
	assert.Equal(t, 0, stats.Count)
	assert.Equal(t, int64(1), stats.Evictions)
	assert.NotZero(t, stats.StartTime)
}

func TestStatsStaysConsistentUnderConcurrency(t *testing.T) {
	rb := NewRingBuffer(50)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			rb.Push(newTestEntry("x"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = rb.Stats()
		}
	}()
	wg.Wait()

	stats := rb.Stats()
	assert.LessOrEqual(t, stats.Count, 50)
	assert.GreaterOrEqual(t, stats.Evictions, int64(150))
	assert.GreaterOrEqual(t, stats.BytesCaptured, int64(0))
}

func TestListReturnsOldestFirst(t *testing.T) {
	rb := NewRingBuffer(10)
	for _, id := range []string{"a", "b", "c"} {
		rb.Push(newTestEntry(id))
	}

	got := rb.List(ListOpts{})
	require.Len(t, got, 3)
	assert.Equal(t, EntryID("a"), got[0].ID)
	assert.Equal(t, EntryID("b"), got[1].ID)
	assert.Equal(t, EntryID("c"), got[2].ID)
}

func TestListOrderPreservedAcrossRingWrap(t *testing.T) {
	rb := NewRingBuffer(3)
	for _, id := range []string{"a", "b", "c", "d", "e"} { // a and b evicted
		rb.Push(newTestEntry(id))
	}

	got := rb.List(ListOpts{})
	require.Len(t, got, 3)
	assert.Equal(t, EntryID("c"), got[0].ID, "order must be oldest→newest even after wrap-around")
	assert.Equal(t, EntryID("d"), got[1].ID)
	assert.Equal(t, EntryID("e"), got[2].ID)
}

func TestListOffsetBeyondRange(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))

	assert.Empty(t, rb.List(ListOpts{Offset: 5}), "offset past the end must yield nothing")
}

func TestListCombinedFilters(t *testing.T) {
	rb := NewRingBuffer(10)
	e1 := newTestEntry("a") // GET 200
	e2 := newTestEntry("b")
	e2.Method = "POST"
	e2.StatusCode = 201
	e3 := newTestEntry("c")
	e3.Method = "POST"
	e3.StatusCode = 500
	rb.Push(e1)
	rb.Push(e2)
	rb.Push(e3)

	got := rb.List(ListOpts{Method: "POST"})
	require.Len(t, got, 2)

	got = rb.List(ListOpts{Method: "POST", Status: 500})
	require.Len(t, got, 1)
	assert.Equal(t, EntryID("c"), got[0].ID)

	got = rb.List(ListOpts{Method: "DELETE"})
	assert.Empty(t, got)
}

func TestGetAfterClear(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))
	rb.Clear()

	assert.Nil(t, rb.Get(EntryID("a")))
	assert.Zero(t, rb.Len())
}

func TestPushPreservesCallerTimestamp(t *testing.T) {
	rb := NewRingBuffer(10)
	when := time.Now().Add(-time.Hour)
	e := newTestEntry("a")
	e.Timestamp = when

	rb.Push(e)

	assert.Equal(t, when, rb.Get(EntryID("a")).Timestamp,
		"Push must not clobber a caller-set timestamp (only stamps zero values)")
}

func TestPushStampsZeroTimestamp(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))
	assert.False(t, rb.Get(EntryID("a")).Timestamp.IsZero(), "entries without a timestamp get one on push")
}

func TestGetEvictedReturnsNil(t *testing.T) {
	rb := NewRingBuffer(2)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))
	rb.Push(newTestEntry("c")) // evicts "a"

	assert.Nil(t, rb.Get(EntryID("a")), "evicted entry must be gone from the index")
	assert.NotNil(t, rb.Get(EntryID("b")))
	assert.NotNil(t, rb.Get(EntryID("c")))
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
