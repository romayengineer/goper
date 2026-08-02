package capture

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
