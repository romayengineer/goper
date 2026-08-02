package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
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
