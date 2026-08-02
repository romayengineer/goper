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

	finalizeEntry(entry)

	if rb.count == rb.capacity {
		rb.evictOldest()
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

	notifySubscribers(subs, entry)
}

// finalizeEntry assigns a default ID and timestamp to an entry that lacks them.
func finalizeEntry(entry *CapturedEntry) {
	if entry.ID == "" {
		entry.ID = NewEntryID()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
}

// evictOldest drops the oldest entry when the ring is full.
func (rb *RingBuffer) evictOldest() {
	delete(rb.idIndex, rb.buffer[rb.head].ID)
	rb.buffer[rb.head] = nil
	rb.head = (rb.head + 1) % rb.capacity
	rb.count--
	rb.evictions++
}

// notifySubscribers delivers a copy of the entry to every subscriber without
// blocking ingestion.
func notifySubscribers(subs []chan *CapturedEntry, entry *CapturedEntry) {
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

	return paginate(rb.scan(opts), opts)
}

// scan collects the matching entries (caller holds the read lock).
func (rb *RingBuffer) scan(opts ListOpts) []*CapturedEntry {
	result := make([]*CapturedEntry, 0, rb.count)
	for i := 0; i < rb.capacity; i++ {
		idx := (rb.head + i) % rb.capacity
		if rb.keep(idx, opts) {
			result = append(result, rb.buffer[idx])
		}
	}
	return result
}

// keep reports whether the entry at idx is present and satisfies the filters.
func (rb *RingBuffer) keep(idx int, opts ListOpts) bool {
	entry := rb.buffer[idx]
	return entry != nil && opts.matches(entry)
}

// matches reports whether an entry satisfies the List filters.
func (opts ListOpts) matches(entry *CapturedEntry) bool {
	return !opts.sinceOrMethodExcludes(entry) && !opts.statusOrURLExcludes(entry)
}

func (opts ListOpts) sinceOrMethodExcludes(entry *CapturedEntry) bool {
	return opts.sinceExcludes(entry) || opts.methodExcludes(entry)
}

func (opts ListOpts) statusOrURLExcludes(entry *CapturedEntry) bool {
	return opts.statusExcludes(entry) || opts.urlExcludes(entry)
}

func (opts ListOpts) sinceExcludes(entry *CapturedEntry) bool {
	return !opts.Since.IsZero() && entry.Timestamp.Before(opts.Since)
}

func (opts ListOpts) methodExcludes(entry *CapturedEntry) bool {
	return opts.Method != "" && entry.Method != opts.Method
}

func (opts ListOpts) statusExcludes(entry *CapturedEntry) bool {
	return opts.Status > 0 && entry.StatusCode != opts.Status
}

func (opts ListOpts) urlExcludes(entry *CapturedEntry) bool {
	return opts.URL != "" && entry.URL != opts.URL
}

// paginate applies offset/limit pagination to a filtered result.
func paginate(result []*CapturedEntry, opts ListOpts) []*CapturedEntry {
	if opts.Offset > 0 {
		if opts.Offset >= len(result) {
			return nil // offset past the end: empty page, not the whole list
		}
		result = result[opts.Offset:]
	}
	return capResult(result, opts.Limit)
}

// capResult truncates result to at most limit entries.
func capResult(result []*CapturedEntry, limit int) []*CapturedEntry {
	if limit > 0 && limit < len(result) {
		return result[:limit]
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
