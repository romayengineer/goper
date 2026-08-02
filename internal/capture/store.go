package capture

import (
	"sync"
	"time"
)

type Store interface {
	Push(entry *CapturedEntry)
	Get(id EntryID) *CapturedEntry
	List(opts ListOpts) []*CapturedEntry
	Clear()
	Len() int
	Stats() StoreStats
	Subscribe() chan *CapturedEntry
	Unsubscribe(ch chan *CapturedEntry)
}

// StoreStats is a point-in-time snapshot of the store, surfaced by the API
// /api/stats endpoint.
type StoreStats struct {
	Count         int       `json:"count"`
	Capacity      int       `json:"capacity"`
	Evictions     int64     `json:"evictions"`
	BytesCaptured int64     `json:"bytes_captured"`
	StartTime     time.Time `json:"start_time"`
}

type RingBuffer struct {
	mu       sync.RWMutex
	buffer   []*CapturedEntry
	head     int
	tail     int
	count    int
	capacity int
	idIndex  map[EntryID]int

	startTime     time.Time
	evictions     int64
	bytesCaptured int64

	subscribers []chan *CapturedEntry
}

func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 10000
	}
	return &RingBuffer{
		buffer:    make([]*CapturedEntry, capacity),
		capacity:  capacity,
		idIndex:   make(map[EntryID]int),
		startTime: time.Now(),
	}
}

func (rb *RingBuffer) Push(entry *CapturedEntry) {
	rb.mu.Lock()

	if entry.ID == "" {
		entry.ID = NewEntryID()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	if rb.count == rb.capacity {
		delete(rb.idIndex, rb.buffer[rb.head].ID)
		rb.buffer[rb.head] = nil
		rb.head = (rb.head + 1) % rb.capacity
		rb.count--
		rb.evictions++
	}
	rb.bytesCaptured += entryBytes(entry)

	rb.buffer[rb.tail] = entry
	rb.idIndex[entry.ID] = rb.tail
	rb.tail = (rb.tail + 1) % rb.capacity
	rb.count++

	// Snapshot the subscribers under the lock, then notify outside it so a
	// slow (or blocking) subscriber never stalls ingestion.
	subs := make([]chan *CapturedEntry, len(rb.subscribers))
	copy(subs, rb.subscribers)
	rb.mu.Unlock()

	entryCopy := *entry
	for _, sub := range subs {
		select {
		case sub <- &entryCopy:
		default:
		}
	}
}

func (rb *RingBuffer) Get(id EntryID) *CapturedEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	idx, ok := rb.idIndex[id]
	if !ok {
		return nil
	}
	entry := rb.buffer[idx]
	if entry == nil {
		return nil
	}
	return entry
}

type ListOpts struct {
	Since  time.Time
	Method string
	Status int
	URL    string
	Limit  int
	Offset int
}

func (rb *RingBuffer) List(opts ListOpts) []*CapturedEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return nil
	}

	var result []*CapturedEntry
	for i := 0; i < rb.capacity; i++ {
		idx := (rb.head + i) % rb.capacity
		entry := rb.buffer[idx]
		if entry == nil {
			continue
		}

		if !opts.Since.IsZero() && entry.Timestamp.Before(opts.Since) {
			continue
		}
		if opts.Method != "" && entry.Method != opts.Method {
			continue
		}
		if opts.Status > 0 && entry.StatusCode != opts.Status {
			continue
		}
		if opts.URL != "" && entry.URL != opts.URL {
			continue
		}

		result = append(result, entry)
	}

	if opts.Offset > 0 {
		if opts.Offset >= len(result) {
			result = nil // offset past the end: empty page, not the whole list
		} else {
			result = result[opts.Offset:]
		}
	}
	if opts.Limit > 0 && opts.Limit < len(result) {
		result = result[:opts.Limit]
	}

	return result
}

func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.buffer = make([]*CapturedEntry, rb.capacity)
	rb.head = 0
	rb.tail = 0
	rb.count = 0
	rb.idIndex = make(map[EntryID]int)
}

func (rb *RingBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}

func (rb *RingBuffer) Stats() StoreStats {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return StoreStats{
		Count:         rb.count,
		Capacity:      rb.capacity,
		Evictions:     rb.evictions,
		BytesCaptured: rb.bytesCaptured,
		StartTime:     rb.startTime,
	}
}

// entryBytes is the number of body bytes actually retained in an entry
// (request + response). Bodies that were skipped (nil) contribute zero.
func entryBytes(e *CapturedEntry) int64 {
	var n int64
	if e.RequestBody != nil {
		n += int64(len(*e.RequestBody))
	}
	if e.ResponseBody != nil {
		n += int64(len(*e.ResponseBody))
	}
	return n
}

func (rb *RingBuffer) Subscribe() chan *CapturedEntry {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	ch := make(chan *CapturedEntry, 100)
	rb.subscribers = append(rb.subscribers, ch)
	return ch
}

func (rb *RingBuffer) Unsubscribe(ch chan *CapturedEntry) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	for i, sub := range rb.subscribers {
		if sub == ch {
			rb.subscribers = append(rb.subscribers[:i], rb.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}
