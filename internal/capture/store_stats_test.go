package capture

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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
