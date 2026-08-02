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
	if r.RequestBodyLimit > 0 && entry.RequestBody != nil && int64(len(*entry.RequestBody)) > r.RequestBodyLimit {
		entry.RequestBody = nil
	}
	return entry
}

func (r *DefaultRecorder) CaptureResponse(statusCode int, header http.Header, bodyBytes []byte, start time.Time) CaptureResult {
	result := CaptureResponse(statusCode, header, bodyBytes, start)
	if r.ResponseBodyLimit > 0 && result.ResponseBody != nil && int64(len(*result.ResponseBody)) > r.ResponseBodyLimit {
		result.ResponseBody = nil
	}
	return result
}

func (r *DefaultRecorder) CombineEntry(reqEntry CapturedEntry, result CaptureResult) *CapturedEntry {
	return CombineEntry(reqEntry, result)
}

func headersToMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, v := range h {
		lower := strings.ToLower(k)
		if redactHeader(lower) {
			m[k] = "***REDACTED***"
			continue
		}
		if len(v) == 1 {
			m[k] = v[0]
		} else {
			m[k] = strings.Join(v, ", ")
		}
	}
	return m
}

// redactHeader reports whether a header carries credentials that must not be
// persisted in captured entries.
func redactHeader(key string) bool {
	return key == "authorization" || key == "cookie" || key == "set-cookie" || key == "proxy-authorization"
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

	if len(bodyBytes) > 0 {
		s := string(bodyBytes)
		if isPrintable(s) || IsJSONContentType(cr.ContentType) {
			cr.ResponseBody = &s
		}
	}

	return cr
}

// IsJSONContentType reports whether a Content-Type value denotes JSON,
// following RFC 6839: any media type with a "+json" structured syntax suffix
// is JSON, plus the common exact types (application/json, text/json) and
// newline-delimited JSON (application/x-ndjson). Parameters such as
// "charset=utf-8" are ignored.
func IsJSONContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return isJSONMimeType(strings.TrimSpace(ct))
}

// isJSONMimeType reports whether a mime type (no parameters) denotes JSON.
func isJSONMimeType(ct string) bool {
	return ct == "application/json" ||
		ct == "text/json" ||
		strings.HasSuffix(ct, "+json") ||
		strings.Contains(ct, "ndjson")
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
// range, or a control character we tolerate in captured bodies (newline,
// carriage return, tab).
func isPrintableChar(r rune) bool {
	return r >= 32 || r == '\n' || r == '\r' || r == '\t'
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
