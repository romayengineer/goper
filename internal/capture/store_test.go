package capture

import (
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

func TestClear(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Push(newTestEntry("a"))
	rb.Push(newTestEntry("b"))

	rb.Clear()

	assert.Equal(t, 0, rb.Len())
	assert.Nil(t, rb.Get("a"), "expected entry to be gone after clear")
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
