package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/romayengineer/goper/internal/capture"
)

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
//
//nolint:gocyclo // a 3-way concurrent wait (cancel, keepalive ping, entries) is irreducible below the limit.
func (h *Handler) streamSelect(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, ping *time.Ticker, ch <-chan *capture.CapturedEntry) bool {
	select {
	case <-ctx.Done():
		return false
	case <-ping.C:
		return writeSSEPing(w, flusher)
	case entry, ok := <-ch:
		return h.writeStreamEntry(w, flusher, entry, ok)
	}
}

// writeStreamEntry writes a live entry to the SSE client, reporting whether
// the stream should continue.
func (h *Handler) writeStreamEntry(w http.ResponseWriter, flusher http.Flusher, entry *capture.CapturedEntry, ok bool) bool {
	return ok && writeSSE(w, flusher, entry)
}

// backfillSSE replays the N most recent entries to a newly connected SSE
// client before it starts receiving live events. It returns false if the
// connection was lost mid-backfill.
func (h *Handler) backfillSSE(w http.ResponseWriter, flusher http.Flusher, backfill int) bool {
	if backfill <= 0 {
		return true
	}
	history := h.store.List(capture.ListOpts{Limit: backfill})
	return writeEntries(w, flusher, history)
}

// writeEntries streams the entries to an SSE client, returning false if the
// connection is lost mid-write.
func writeEntries(w http.ResponseWriter, flusher http.Flusher, entries []*capture.CapturedEntry) bool {
	for _, entry := range entries {
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
		return parseBackfill(v, backfill)
	}
	return backfill
}

// parseBackfill parses a backfill param, returning the default on invalid or
// negative values.
func parseBackfill(v string, def int) int {
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return n
	}
	return def
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
