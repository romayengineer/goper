package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/romayengineer/goper/internal/capture"
)

type Handler struct {
	store capture.Store
	caPEM []byte
}

func NewHandler(store capture.Store, caPEM []byte) *Handler {
	return &Handler{store: store, caPEM: caPEM}
}

func (h *Handler) ListRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	opts := capture.ListOpts{}

	if since := q.Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			opts.Since = t
		}
	}
	if method := q.Get("method"); method != "" {
		opts.Method = method
	}
	if statusStr := q.Get("status"); statusStr != "" {
		if status, err := strconv.Atoi(statusStr); err == nil {
			opts.Status = status
		}
	}
	if url := q.Get("url"); url != "" {
		opts.URL = url
	}
	if limitStr := q.Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
			opts.Limit = limit
		}
	}
	if offsetStr := q.Get("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil && offset > 0 {
			opts.Offset = offset
		}
	}

	entries := h.store.List(opts)
	if entries == nil {
		entries = []*capture.CapturedEntry{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"count": len(entries),
		"data":  entries,
	})
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

func (h *Handler) StreamRequests(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := h.store.Subscribe()
	defer h.store.Unsubscribe(ch)

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (h *Handler) ClearRequests(w http.ResponseWriter, r *http.Request) {
	h.store.Clear()
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"count": h.store.Len(),
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
	json.NewEncoder(w).Encode(v)
}
