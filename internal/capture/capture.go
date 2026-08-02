package capture

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"
)

type Recorder interface {
	CaptureRequest(r *http.Request) CapturedEntry
	CaptureResponse(statusCode int, header http.Header, bodyBytes []byte, start time.Time) CaptureResult
	CombineEntry(reqEntry CapturedEntry, result CaptureResult) *CapturedEntry
}

// DefaultRecorder captures requests and responses, honoring optional body
// size limits. A limit of 0 means unlimited. Response bodies that exceed the
// limit are skipped (kept nil); the request/response still proxies normally.
type DefaultRecorder struct {
	RequestBodyLimit  int64
	ResponseBodyLimit int64
}

// NewDefaultRecorder builds a recorder with the given body size limits
// (bytes; 0 = unlimited).
func NewDefaultRecorder(requestBodyLimit, responseBodyLimit int64) Recorder {
	return &DefaultRecorder{
		RequestBodyLimit:  requestBodyLimit,
		ResponseBodyLimit: responseBodyLimit,
	}
}

func (r *DefaultRecorder) CaptureRequest(req *http.Request) CapturedEntry {
	entry := captureRequest(req, r.RequestBodyLimit)
	if entry.RequestBody != nil && overLimit(r.RequestBodyLimit, len(*entry.RequestBody)) {
		entry.RequestBody = nil
	}
	return entry
}

func (r *DefaultRecorder) CaptureResponse(statusCode int, header http.Header, bodyBytes []byte, start time.Time) CaptureResult {
	result := CaptureResponse(statusCode, header, bodyBytes, start)
	if result.ResponseBody != nil && overLimit(r.ResponseBodyLimit, len(*result.ResponseBody)) {
		result.ResponseBody = nil
	}
	return result
}

// overLimit reports whether a captured body of length n exceeds a capture
// limit (0 = unlimited).
func overLimit(limit int64, n int) bool {
	return limit > 0 && int64(n) > limit
}

func (r *DefaultRecorder) CombineEntry(reqEntry CapturedEntry, result CaptureResult) *CapturedEntry {
	return CombineEntry(reqEntry, result)
}

func headersToMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		if redactHeader(strings.ToLower(k)) {
			m[k] = "***REDACTED***"
			continue
		}
		m[k] = joinValues(v)
	}
	return m
}

// joinValues joins a header value list.
func joinValues(v []string) string {
	if len(v) == 1 {
		return v[0]
	}
	return strings.Join(v, ", ")
}

// redactedHeaders are header names carrying credentials that must never be
// persisted in captured entries.
var redactedHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"set-cookie":          true,
	"proxy-authorization": true,
}

// redactHeader reports whether a header carries credentials that must not be
// persisted in captured entries.
func redactHeader(key string) bool {
	return redactedHeaders[key]
}

func CaptureRequest(req *http.Request) CapturedEntry {
	return captureRequest(req, -1)
}

// captureRequest builds a request entry, reading at most limit+1 bytes of the
// request body (limit <= 0 means unlimited). Reading is bounded so a large
// upload is never fully buffered when a limit is configured. The body is
// restored as the captured prefix plus the untouched remainder so proxying
// still sees the complete request body.
func captureRequest(req *http.Request, limit int64) CapturedEntry {
	entry := CapturedEntry{
		Method:         req.Method,
		URL:            req.URL.String(),
		Scheme:         req.URL.Scheme,
		Host:           req.URL.Host,
		Path:           req.URL.Path,
		RequestHeaders: headersToMap(req.Header),
	}

	if req.Body != nil {
		buffered, err := readBounded(req.Body, limit)
		if err == nil {
			s := string(buffered)
			entry.RequestBody = &s
			req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buffered), req.Body))
		}
	}

	return entry
}

// readBounded reads at most limit+1 bytes from r, leaving any remainder in r
// untouched (reads stop when the limit is reached). limit <= 0 reads
// everything (the "0 = unlimited" config convention). It returns the bytes
// read (never more than limit+1).
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(r)
	}
	return io.ReadAll(io.LimitReader(r, limit+1))
}

type CaptureResult struct {
	StatusCode      int
	ResponseHeaders map[string]string
	ResponseBody    *string
	ContentType     string
	DurationMs      int64
}

func CaptureResponse(statusCode int, header http.Header, bodyBytes []byte, start time.Time) CaptureResult {
	cr := CaptureResult{
		StatusCode:      statusCode,
		ResponseHeaders: headersToMap(header),
		DurationMs:      time.Since(start).Milliseconds(),
		ContentType:     header.Get("Content-Type"),
	}

	cr.ResponseBody = captureBody(bodyBytes, cr.ContentType)

	return cr
}

// captureBody returns the body string when it is worth capturing.
func captureBody(bodyBytes []byte, contentType string) *string {
	if len(bodyBytes) == 0 {
		return nil
	}
	s := string(bodyBytes)
	if worthCapturing(s, contentType) {
		return &s
	}
	return nil
}

// worthCapturing reports whether a response body should be persisted.
func worthCapturing(s, contentType string) bool {
	return isPrintable(s) || IsJSONContentType(contentType)
}

// IsJSONContentType reports whether a Content-Type value denotes JSON,
// following RFC 6839: any media type with a "+json" structured syntax suffix
// is JSON, plus the common exact types (application/json, text/json) and
// newline-delimited JSON (application/x-ndjson). Parameters such as
// "charset=utf-8" are ignored.
func IsJSONContentType(contentType string) bool {
	return isJSONMimeType(NormalizeMediaType(contentType))
}

// NormalizeMediaType lowercases a Content-Type value and strips its parameters
// ("application/json; charset=utf-8" → "application/json").
func NormalizeMediaType(contentType string) string {
	ct := strings.ToLower(contentType)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct)
}

// isJSONMimeType reports whether a mime type (no parameters) denotes JSON.
func isJSONMimeType(ct string) bool {
	return ct == "application/json" || ct == "text/json" ||
		hasJSONMarker(ct)
}

// hasJSONMarker reports the +json suffix or ndjson marker.
func hasJSONMarker(ct string) bool {
	return strings.HasSuffix(ct, "+json") || strings.Contains(ct, "ndjson")
}

func isPrintable(s string) bool {
	for _, r := range s {
		if !isPrintableChar(r) {
			return false
		}
	}
	return true
}

// isPrintableChar reports whether r is printable: at or above the printable
// range, or a control character we tolerate in captured bodies.
func isPrintableChar(r rune) bool {
	return r >= 32 || isToleratedControl(r)
}

// isToleratedControl reports the control characters we keep in captured bodies
// (newline, carriage return, tab).
func isToleratedControl(r rune) bool {
	return r == '\n' || r == '\r' || r == '\t'
}

func CombineEntry(reqEntry CapturedEntry, result CaptureResult) *CapturedEntry {
	reqEntry.StatusCode = result.StatusCode
	reqEntry.ResponseHeaders = result.ResponseHeaders
	reqEntry.ResponseBody = result.ResponseBody
	reqEntry.ContentType = result.ContentType
	reqEntry.DurationMs = result.DurationMs

	reqEntry.ID = NewEntryID()
	reqEntry.Timestamp = time.Now()

	return &reqEntry
}
