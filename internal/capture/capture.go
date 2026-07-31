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

type DefaultRecorder struct{}

func (DefaultRecorder) CaptureRequest(r *http.Request) CapturedEntry {
	return CaptureRequest(r)
}

func (DefaultRecorder) CaptureResponse(statusCode int, header http.Header, bodyBytes []byte, start time.Time) CaptureResult {
	return CaptureResponse(statusCode, header, bodyBytes, start)
}

func (DefaultRecorder) CombineEntry(reqEntry CapturedEntry, result CaptureResult) *CapturedEntry {
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

func IsJSONContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "application/json") ||
		strings.Contains(ct, "application/vnd.api+json") ||
		strings.Contains(ct, "application/problem+json")
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
