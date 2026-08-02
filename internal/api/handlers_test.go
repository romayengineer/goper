package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStore struct {
	mu          sync.Mutex
	entries     []*capture.CapturedEntry
	subscribers []chan *capture.CapturedEntry
	cleared     bool
}

func (m *mockStore) Push(entry *capture.CapturedEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	for _, ch := range m.subscribers {
		select {
		case ch <- entry:
		default:
		}
	}
}

func (m *mockStore) Get(id capture.EntryID) *capture.CapturedEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (m *mockStore) List(opts capture.ListOpts) []*capture.CapturedEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*capture.CapturedEntry
	for _, e := range m.entries {
		if !opts.Since.IsZero() && e.Timestamp.Before(opts.Since) {
			continue
		}
		if opts.Method != "" && e.Method != opts.Method {
			continue
		}
		if opts.Status > 0 && e.StatusCode != opts.Status {
			continue
		}
		if opts.URL != "" && e.URL != opts.URL {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (m *mockStore) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleared = true
	m.entries = nil
}

func (m *mockStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

func (m *mockStore) Stats() capture.StoreStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return capture.StoreStats{
		Count:     len(m.entries),
		Capacity:  10000,
		StartTime: time.Now(),
	}
}

func (m *mockStore) Subscribe() chan *capture.CapturedEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan *capture.CapturedEntry, 10)
	m.subscribers = append(m.subscribers, ch)
	return ch
}

func (m *mockStore) Unsubscribe(ch chan *capture.CapturedEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.subscribers {
		if s == ch {
			m.subscribers = append(m.subscribers[:i], m.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}

func newHandler(store *mockStore, caPEM []byte) *Handler {
	return NewHandler(store, caPEM)
}

func sampleEntry(id string) *capture.CapturedEntry {
	return &capture.CapturedEntry{
		ID:         capture.EntryID(id),
		Timestamp:  time.Now(),
		Method:     "GET",
		URL:        "http://example.com/" + id,
		StatusCode: 200,
	}
}

func decodeList(t *testing.T, resp *httptest.ResponseRecorder) (int, []*capture.CapturedEntry) {
	t.Helper()
	var body struct {
		Count int                      `json:"count"`
		Data  []*capture.CapturedEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	return body.Count, body.Data
}

func TestListRequests(t *testing.T) {
	store := &mockStore{
		entries: []*capture.CapturedEntry{sampleEntry("a"), sampleEntry("b")},
	}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	count, data := decodeList(t, rec)
	assert.Equal(t, 2, count)
	assert.Len(t, data, 2)
}

func TestListRequestsEmpty(t *testing.T) {
	store := &mockStore{}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	count, data := decodeList(t, rec)
	assert.Equal(t, 0, count)
	assert.Empty(t, data)
}

func TestListRequestsFilters(t *testing.T) {
	post := sampleEntry("post")
	post.Method = "POST"
	store := &mockStore{
		entries: []*capture.CapturedEntry{sampleEntry("get"), post},
	}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests?method=POST", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	count, data := decodeList(t, rec)
	assert.Equal(t, 1, count)
	require.Len(t, data, 1)
	assert.Equal(t, "post", string(data[0].ID))
}

func TestListRequestsInvalidParamsIgnored(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a")}}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests?since=not-a-date&status=abc&limit=-5&offset=-1", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	count, _ := decodeList(t, rec)
	assert.Equal(t, 1, count, "invalid params should be ignored")
}

func TestGetRequestFound(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("abc")}}
	h := newHandler(store, nil)

	r := chi.NewRouter()
	r.Get("/api/requests/{id}", h.GetRequest)

	req := httptest.NewRequest(http.MethodGet, "/api/requests/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var entry capture.CapturedEntry
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entry))
	assert.Equal(t, "abc", string(entry.ID))
}

func TestGetRequestNotFound(t *testing.T) {
	store := &mockStore{}
	h := newHandler(store, nil)

	r := chi.NewRouter()
	r.Get("/api/requests/{id}", h.GetRequest)

	req := httptest.NewRequest(http.MethodGet, "/api/requests/missing", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestClearRequests(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a")}}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/requests", nil)
	rec := httptest.NewRecorder()
	h.ClearRequests(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, store.cleared, "expected store.Clear to be called")
}

func TestGetStats(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a"), sampleEntry("b")}}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rec := httptest.NewRecorder()
	h.GetStats(rec, req)

	var body struct {
		Count         int   `json:"count"`
		Capacity      int   `json:"capacity"`
		Evictions     int64 `json:"evictions"`
		BytesCaptured int64 `json:"bytes_captured"`
		UptimeSeconds int64 `json:"uptime_seconds"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 2, body.Count)
	assert.Equal(t, 10000, body.Capacity)
	assert.Zero(t, body.Evictions)
	assert.Zero(t, body.BytesCaptured)
	assert.GreaterOrEqual(t, body.UptimeSeconds, int64(0))
}

func TestStreamRequestsBackfill(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("old1"), sampleEntry("old2")}}
	h := newHandler(store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream?backfill=2", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamRequests(rec, req)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	assert.Contains(t, body, `"id":"old1"`, "expected historical entry in backfill")
	assert.Contains(t, body, `"id":"old2"`, "expected historical entry in backfill")
}

func TestStreamRequestsBackfillDisabled(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("old1")}}
	h := newHandler(store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream?backfill=0", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamRequests(rec, req)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	assert.NotContains(t, rec.Body.String(), "old1", "backfill=0 must not send history")
}

func TestReplayRequest(t *testing.T) {
	var gotHost, gotMethod, gotBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"replayed":true}`))
	}))
	defer target.Close()

	body := `{"x":1}`
	entry := sampleEntry("abc")
	entry.Method = "POST"
	entry.URL = target.URL
	entry.RequestBody = &body
	entry.RequestHeaders = map[string]string{
		"Content-Type": "application/json",
		"X-Trace":      "abc",
		"Host":         "stale.example.com", // hop-by-hop: must be stripped
	}

	h := newHandler(&mockStore{entries: []*capture.CapturedEntry{entry}}, nil)

	r := chi.NewRouter()
	r.Post("/api/requests/{id}/replay", h.ReplayRequest)

	req := httptest.NewRequest(http.MethodPost, "/api/requests/abc/replay", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, `{"replayed":true}`, resp.Body)

	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, `{"x":1}`, gotBody)
	assert.NotEqual(t, "stale.example.com", gotHost, "stale Host header must be stripped on replay")
}

func TestReplayRequestNotFound(t *testing.T) {
	h := newHandler(&mockStore{}, nil)

	r := chi.NewRouter()
	r.Post("/api/requests/{id}/replay", h.ReplayRequest)

	req := httptest.NewRequest(http.MethodPost, "/api/requests/nope/replay", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetCA(t *testing.T) {
	pem := []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----")
	h := newHandler(&mockStore{}, pem)

	req := httptest.NewRequest(http.MethodGet, "/api/ca.pem", nil)
	rec := httptest.NewRecorder()
	h.GetCA(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, string(pem), rec.Body.String())
	assert.Equal(t, "application/x-pem-file", rec.Header().Get("Content-Type"))
}

func TestGetCANotFound(t *testing.T) {
	h := newHandler(&mockStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/ca.pem", nil)
	rec := httptest.NewRecorder()
	h.GetCA(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushRecorder) Flush() {}

func TestStreamRequests(t *testing.T) {
	store := &mockStore{}
	h := newHandler(store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream", nil).WithContext(ctx)
	rec := &flushRecorder{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamRequests(rec, req)
	}()

	time.Sleep(50 * time.Millisecond)
	store.Push(sampleEntry("live"))
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream to close")
	}

	assert.Contains(t, rec.Body.String(), "data: ")
}

type nonFlusher struct {
	http.ResponseWriter
}

func TestStreamRequestsNotFlusher(t *testing.T) {
	h := newHandler(&mockStore{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream", nil)
	rec := httptest.NewRecorder()
	h.StreamRequests(nonFlusher{rec}, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandlerImplementsRequestHandler(t *testing.T) {
	var _ RequestHandler = newHandler(&mockStore{}, nil)
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]string{"ok": "true"})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

type failingSSEWriter struct {
	*httptest.ResponseRecorder
}

func (failingSSEWriter) Flush() {}

func (failingSSEWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}

// TestStreamRequestsStopsOnWriteError covers the SSE error path: when the
// client connection can no longer accept writes, the handler must stop
// instead of looping forever.
func TestStreamRequestsStopsOnWriteError(t *testing.T) {
	store := &mockStore{entries: []*capture.CapturedEntry{sampleEntry("a")}}
	h := newHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests/stream", nil)
	rec := &failingSSEWriter{httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.StreamRequests(rec, req)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler did not stop when writes failed")
	}
}

// ---- tests below exercise the API against the real RingBuffer ----

func TestListRequestsPaginationThroughAPI(t *testing.T) {
	store := capture.NewRingBuffer(100)
	for i := 0; i < 5; i++ {
		store.Push(sampleEntry(string(rune('a' + i))))
	}
	h := NewHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests?limit=2&offset=2", nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	count, data := decodeList(t, rec)
	assert.Equal(t, 2, count)
	require.Len(t, data, 2)
	assert.Equal(t, "c", string(data[0].ID))
	assert.Equal(t, "d", string(data[1].ID))
}

func TestListRequestsFiltersThroughAPI(t *testing.T) {
	store := capture.NewRingBuffer(100)
	post := sampleEntry("post")
	post.Method = "POST"
	post.StatusCode = 201
	post.URL = "http://example.com/items"
	store.Push(sampleEntry("get"))
	store.Push(post)
	h := NewHandler(store, nil)

	cases := []struct {
		query  string
		wantID string
	}{
		{"?method=POST", "post"},
		{"?status=201", "post"},
		{"?url=http://example.com/items", "post"},
		{"?method=GET", "get"},
		{"?method=DELETE", ""}, // no match
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/requests"+tc.query, nil)
		rec := httptest.NewRecorder()
		h.ListRequests(rec, req)
		count, data := decodeList(t, rec)
		if tc.wantID == "" {
			assert.Zero(t, count, "query %s", tc.query)
			continue
		}
		require.Equal(t, 1, count, "query %s", tc.query)
		assert.Equal(t, tc.wantID, string(data[0].ID), "query %s", tc.query)
	}
}

func TestListRequestsSinceThroughAPI(t *testing.T) {
	store := capture.NewRingBuffer(100)
	old := sampleEntry("old")
	old.Timestamp = time.Now().Add(-time.Hour)
	store.Push(old)
	store.Push(sampleEntry("new"))
	h := NewHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/requests?since="+time.Now().Add(-time.Minute).Format(time.RFC3339), nil)
	rec := httptest.NewRecorder()
	h.ListRequests(rec, req)

	count, data := decodeList(t, rec)
	assert.Equal(t, 1, count)
	require.Len(t, data, 1)
	assert.Equal(t, "new", string(data[0].ID))
}

func TestGetStatsThroughRealStore(t *testing.T) {
	store := capture.NewRingBuffer(10)
	body := `{"x":1}` // 7 bytes
	e := sampleEntry("a")
	e.RequestBody = &body
	e.ResponseBody = &body
	store.Push(e)
	h := NewHandler(store, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rec := httptest.NewRecorder()
	h.GetStats(rec, req)

	var s struct {
		Count         int   `json:"count"`
		Capacity      int   `json:"capacity"`
		BytesCaptured int64 `json:"bytes_captured"`
		UptimeSeconds int64 `json:"uptime_seconds"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
	assert.Equal(t, 1, s.Count)
	assert.Equal(t, 10, s.Capacity)
	assert.Equal(t, int64(14), s.BytesCaptured, "request + response bodies")
	assert.GreaterOrEqual(t, s.UptimeSeconds, int64(0))
}

func TestStreamRequestsBackfillCountsWithRealStore(t *testing.T) {
	store := capture.NewRingBuffer(100)
	for i := 0; i < 3; i++ {
		store.Push(sampleEntry(string(rune('a' + i))))
	}
	h := NewHandler(store, nil)

	cases := []struct {
		query    string
		wantData int
	}{
		{"?backfill=2", 2},
		{"?backfill=0", 0},
		{"?backfill=abc", 3}, // invalid → default 50, only 3 exist
		{"?backfill=-1", 3},  // negative → default 50, only 3 exist
		{"", 3},              // default backfill
	}
	for _, tc := range cases {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/api/requests/stream"+tc.query, nil).WithContext(ctx)
		rec := &flushRecorder{httptest.NewRecorder()}
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.StreamRequests(rec, req)
		}()
		time.Sleep(50 * time.Millisecond)
		cancel()
		<-done

		got := strings.Count(rec.Body.String(), "data: ")
		assert.Equal(t, tc.wantData, got, "query %s", tc.query)
	}
}

func TestReplayRequestGETEntry(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("fresh"))
	}))
	defer target.Close()

	entry := sampleEntry("abc")
	entry.Method = "GET"
	entry.URL = target.URL
	entry.RequestHeaders = map[string]string{"Accept": "text/plain"}
	// RequestBody is nil — replay must still work for bodyless requests.

	h := NewHandler(&mockStore{entries: []*capture.CapturedEntry{entry}}, nil)

	r := chi.NewRouter()
	r.Post("/api/requests/{id}/replay", h.ReplayRequest)

	req := httptest.NewRequest(http.MethodPost, "/api/requests/abc/replay", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "fresh", resp.Body)
}
