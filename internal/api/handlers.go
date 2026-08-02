package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/romayengineer/goper/internal/capture"
)

type RequestHandler interface {
	ListRequests(w http.ResponseWriter, r *http.Request)
	GetRequest(w http.ResponseWriter, r *http.Request)
	StreamRequests(w http.ResponseWriter, r *http.Request)
	ClearRequests(w http.ResponseWriter, r *http.Request)
	GetStats(w http.ResponseWriter, r *http.Request)
	GetCA(w http.ResponseWriter, r *http.Request)
	ReplayRequest(w http.ResponseWriter, r *http.Request)
}

type Handler struct {
	store capture.Store
	caPEM []byte
}

func NewHandler(store capture.Store, caPEM []byte) *Handler {
	return &Handler{store: store, caPEM: caPEM}
}

func (h *Handler) ListRequests(w http.ResponseWriter, r *http.Request) {
	entries := h.store.List(listOptsFromQuery(r.URL.Query()))
	if entries == nil {
		entries = []*capture.CapturedEntry{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"count": len(entries),
		"data":  entries,
	})
}

// listOptsFromQuery parses the /api/requests query string into list filters.
func listOptsFromQuery(q url.Values) capture.ListOpts {
	return capture.ListOpts{
		Since:  parseTimeQuery(q, "since"),
		Method: q.Get("method"),
		Status: parseIntQuery(q, "status"),
		URL:    q.Get("url"),
		Limit:  parsePositiveIntQuery(q, "limit"),
		Offset: parsePositiveIntQuery(q, "offset"),
	}
}

// parseTimeQuery reads an RFC3339 timestamp query param, returning the zero
// time when absent or unparsable.
func parseTimeQuery(q url.Values, key string) time.Time {
	raw := q.Get(key)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

// parseIntQuery reads an integer query param, returning 0 when absent or
// unparsable.
func parseIntQuery(q url.Values, key string) int {
	n, err := strconv.Atoi(q.Get(key))
	if err != nil {
		return 0
	}
	return n
}

// parsePositiveIntQuery reads an integer query param, returning 0 when absent,
// unparsable, or not strictly positive.
func parsePositiveIntQuery(q url.Values, key string) int {
	n, err := strconv.Atoi(q.Get(key))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func (h *Handler) GetRequest(w http.ResponseWriter, r *http.Request) {
	id := capture.EntryID(chi.URLParam(r, "id"))
	entry := h.store.Get(id)
	if entry == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "entry not found"})
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// defaultBackfill is how many most-recent historical entries a new SSE
// client receives before live events, unless overridden via ?backfill=N
// (0 disables backfill entirely).
const defaultBackfill = 50

func (h *Handler) StreamRequests(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Backfill: send the most recent entries first so a client that connects
	// mid-stream catches up. Entries captured between this snapshot and the
	// Subscribe below may be sent twice (snapshot then live) — harmless for
	// idempotent consumers; avoiding it would require a per-client cursor.
	if !h.backfillSSE(w, flusher, backfillCount(r)) {
		return
	}

	ch := h.store.Subscribe()
	defer h.store.Unsubscribe(ch)

	h.streamLoop(r.Context(), w, flusher, ch)
}

// streamLoop forwards live entries to the SSE client until the connection
// closes. A periodic comment keeps the stream alive through proxies/gateways
// that reap idle connections.
func (h *Handler) streamLoop(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, ch <-chan *capture.CapturedEntry) {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for h.streamSelect(ctx, w, flusher, ping, ch) {
	}
}

// streamSelect handles one event from the live stream and reports whether the
// connection is still healthy.
func (h *Handler) streamSelect(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, ping *time.Ticker, ch <-chan *capture.CapturedEntry) bool {
	select {
	case <-ctx.Done():
		return false
	case <-ping.C:
		return writeSSEPing(w, flusher)
	case entry, ok := <-ch:
		return ok && writeSSE(w, flusher, entry)
	}
}

// backfillSSE replays the N most recent entries to a newly connected SSE
// client before it starts receiving live events. It returns false if the
// connection was lost mid-backfill.
func (h *Handler) backfillSSE(w http.ResponseWriter, flusher http.Flusher, backfill int) bool {
	if backfill <= 0 {
		return true
	}
	history := h.store.List(capture.ListOpts{Limit: backfill})
	for _, entry := range history {
		if !writeSSE(w, flusher, entry) {
			return false
		}
	}
	return true
}

// backfillCount reads the ?backfill=N parameter (defaultBackfill when absent
// or invalid; 0 disables backfill).
func backfillCount(r *http.Request) int {
	backfill := defaultBackfill
	if v := r.URL.Query().Get("backfill"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			backfill = n
		}
	}
	return backfill
}

// writeSSEPing writes a keepalive comment and flushes; returns false on write
// failure (client disconnected).
func writeSSEPing(w http.ResponseWriter, flusher http.Flusher) bool {
	if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, entry *capture.CapturedEntry) bool {
	data, err := json.Marshal(entry)
	if err != nil {
		slog.Error("sse marshal failed", "id", entry.ID, "error", err)
		return true
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func (h *Handler) ClearRequests(w http.ResponseWriter, r *http.Request) {
	h.store.Clear()
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	s := h.store.Stats()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"count":          s.Count,
		"capacity":       s.Capacity,
		"evictions":      s.Evictions,
		"bytes_captured": s.BytesCaptured,
		"uptime_seconds": int64(time.Since(s.StartTime).Seconds()),
		"start_time":     s.StartTime,
	})
}

func (h *Handler) GetCA(w http.ResponseWriter, r *http.Request) {
	if h.caPEM == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "CA not available"})
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=goper-ca.pem")
	_, _ = w.Write(h.caPEM)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// replayMaxBody caps how much of a replayed response is returned to the
// caller (the response itself is streamed in full to the upstream target).
const replayMaxBody = 1 << 20 // 1 MiB

// replayClient is shared across replay requests; a single client keeps the
// connection pool warm and avoids re-initializing TLS state per call.
var replayClient = &http.Client{Timeout: 30 * time.Second}

// hopByHopHeaders are stripped when rebuilding a replayed request; they are
// connection-specific and meaningless (or actively harmful) when resent.
var hopByHopHeaders = map[string]bool{
	"connection":        true,
	"proxy-connection":  true,
	"keep-alive":        true,
	"transfer-encoding": true,
	"te":                true,
	"trailer":           true,
	"upgrade":           true,
	"host":              true,
	"content-length":    true,
}

// copyReplayHeaders copies captured request headers onto a replay request,
// skipping hop-by-hop headers.
func copyReplayHeaders(req *http.Request, headers map[string]string) {
	for k, v := range headers {
		if hopByHopHeaders[strings.ToLower(k)] {
			continue
		}
		req.Header.Set(k, v)
	}
}

// ReplayRequest re-sends a captured request to its original URL using the
// stored method, headers and body, and returns the fresh response. Useful for
// re-executing an API call after a fix, or for testing idempotent endpoints.
func (h *Handler) ReplayRequest(w http.ResponseWriter, r *http.Request) {
	id := capture.EntryID(chi.URLParam(r, "id"))
	entry := h.store.Get(id)
	if entry == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "entry not found"})
		return
	}

	var body io.Reader
	if entry.RequestBody != nil {
		body = strings.NewReader(*entry.RequestBody)
	}

	req, err := http.NewRequest(entry.Method, entry.URL, body) // #nosec G704 -- replay is an explicit feature: it resends a captured request to its stored URL
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	copyReplayHeaders(req, entry.RequestHeaders)

	client := replayClient
	start := time.Now()
	resp, err := client.Do(req) // #nosec G704 -- replay is an explicit feature: it resends a captured request to its stored URL
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, replayMaxBody))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(respBody),
		"duration_ms": time.Since(start).Milliseconds(),
	})
}
