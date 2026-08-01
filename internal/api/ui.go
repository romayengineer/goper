package api

import (
	_ "embed"
	"net/http"
)

//go:embed static/index.html
var indexHTML []byte

// serveUI serves the embedded single-file dashboard. It is intentionally a
// plain HTML page with vanilla JS — no build step, no external CDN assets —
// so it works fully offline from any browser pointed at the API port.
func serveUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}
