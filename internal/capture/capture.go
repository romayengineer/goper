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
	entry := CaptureRequest(req)
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
		if lower == "authorization" || lower == "cookie" || lower == "set-cookie" || lower == "proxy-authorization" {
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

func CaptureRequest(req *http.Request) CapturedEntry {
	entry := CapturedEntry{
		Method:         req.Method,
		URL:            req.URL.String(),
		Scheme:         req.URL.Scheme,
		Host:           req.URL.Host,
		Path:           req.URL.Path,
		RequestHeaders: headersToMap(req.Header),
	}

	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			s := string(bodyBytes)
			entry.RequestBody = &s
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
	}

	return entry
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

	if len(bodyBytes) > 0 && len(bodyBytes) < 1*1024*1024 {
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
	ct = strings.TrimSpace(ct)

	return ct == "application/json" ||
		ct == "text/json" ||
		strings.HasSuffix(ct, "+json") ||
		strings.Contains(ct, "ndjson")
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
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
