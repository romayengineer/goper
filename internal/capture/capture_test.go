package capture

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/users?id=1", strings.NewReader(`{"name":"alice"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Custom", "one")

	entry := CaptureRequest(req)

	assert.Equal(t, http.MethodPost, entry.Method)
	assert.Equal(t, "https://example.com/api/users?id=1", entry.URL)
	assert.Equal(t, "https", entry.Scheme)
	assert.Equal(t, "example.com", entry.Host)
	assert.Equal(t, "/api/users", entry.Path)
	require.NotNil(t, entry.RequestBody)
	assert.Equal(t, `{"name":"alice"}`, *entry.RequestBody)
	assert.Equal(t, "***REDACTED***", entry.RequestHeaders["Authorization"])
	assert.Equal(t, "one", entry.RequestHeaders["X-Custom"])

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"name":"alice"}`, string(restored), "body should be restored for re-read")
}

func TestCaptureRequestNoBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	entry := CaptureRequest(req)

	require.NotNil(t, entry.RequestBody)
	assert.Empty(t, *entry.RequestBody)
}

func TestCaptureResponseJSON(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Set-Cookie", "session=abc")

	start := time.Now()
	cr := CaptureResponse(200, header, []byte(`{"ok":true}`), start.Add(-5*time.Millisecond))

	assert.Equal(t, 200, cr.StatusCode)
	assert.Equal(t, "application/json", cr.ContentType)
	require.NotNil(t, cr.ResponseBody)
	assert.Equal(t, `{"ok":true}`, *cr.ResponseBody)
	assert.Equal(t, "***REDACTED***", cr.ResponseHeaders["Set-Cookie"])
	assert.GreaterOrEqual(t, cr.DurationMs, int64(1))
}

func TestCaptureResponsePlainText(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "text/plain")

	cr := CaptureResponse(200, header, []byte("hello world"), time.Now())
	require.NotNil(t, cr.ResponseBody)
	assert.Equal(t, "hello world", *cr.ResponseBody)
}

func TestCaptureResponseLargeBody(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/json")

	big := make([]byte, 2*1024*1024)
	for i := range big {
		big[i] = 'x'
	}

	cr := CaptureResponse(200, header, big, time.Now())
	assert.Nil(t, cr.ResponseBody, "expected large body to be skipped")
}

func TestCaptureResponseBinary(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/octet-stream")

	binary := []byte{0x00, 0x01, 0x02, 0xff, 0x00}
	cr := CaptureResponse(200, header, binary, time.Now())
	assert.Nil(t, cr.ResponseBody, "expected binary body to be skipped")
}

func TestCombineEntry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	reqEntry := CaptureRequest(req)

	result := CaptureResult{
		StatusCode:      201,
		ResponseHeaders: map[string]string{"Content-Type": "application/json"},
		ResponseBody:    strPtr(`{"created":true}`),
		ContentType:     "application/json",
		DurationMs:      42,
	}

	combined := CombineEntry(reqEntry, result)

	assert.Equal(t, 201, combined.StatusCode)
	require.NotNil(t, combined.ResponseBody)
	assert.Equal(t, `{"created":true}`, *combined.ResponseBody)
	assert.Equal(t, int64(42), combined.DurationMs)
	assert.Equal(t, http.MethodGet, combined.Method)
	assert.NotEmpty(t, combined.ID, "expected combined entry to have an ID")
}

func TestDefaultRecorderRequestBodyLimit(t *testing.T) {
	rec := NewDefaultRecorder(10, 0) // 10-byte request cap, unlimited response

	req := httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("this body is way too long"))
	entry := rec.CaptureRequest(req)
	assert.Nil(t, entry.RequestBody, "request body over limit should be skipped")

	reqSmall := httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("short"))
	entrySmall := rec.CaptureRequest(reqSmall)
	require.NotNil(t, entrySmall.RequestBody)
	assert.Equal(t, "short", *entrySmall.RequestBody)

	// The body must still be restored for proxying even when skipped.
	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, "this body is way too long", string(restored))
}

func TestDefaultRecorderZeroRequestLimitUnlimited(t *testing.T) {
	rec := NewDefaultRecorder(0, 0) // 0 = unlimited

	req := httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("a fairly long body"))
	entry := rec.CaptureRequest(req)
	require.NotNil(t, entry.RequestBody)
	assert.Equal(t, "a fairly long body", *entry.RequestBody)
}

func TestDefaultRecorderResponseBodyLimit(t *testing.T) {
	rec := NewDefaultRecorder(0, 5) // 5-byte response cap

	header := http.Header{}
	header.Set("Content-Type", "application/json")

	cr := rec.CaptureResponse(200, header, []byte(`{"ok":true}`), time.Now())
	assert.Nil(t, cr.ResponseBody, "response body over limit should be skipped")

	crSmall := rec.CaptureResponse(200, header, []byte(`{"a"}`), time.Now()) // 5 bytes = at limit
	require.NotNil(t, crSmall.ResponseBody)
	assert.Equal(t, `{"a"}`, *crSmall.ResponseBody)
}

func strPtr(s string) *string {
	return &s
}

func TestIsJSONContentTypeVariants(t *testing.T) {
	for _, ct := range []string{
		"application/json",
		"application/json; charset=utf-8",
		"application/vnd.api+json",
		"application/problem+json",
		"APPLICATION/JSON",
		"application/vnd.example.v1+json",
	} {
		assert.True(t, IsJSONContentType(ct), "expected %q to be JSON", ct)
	}
	for _, ct := range []string{"text/html", "text/plain", "", "application/xml", "application/octet-stream"} {
		assert.False(t, IsJSONContentType(ct), "expected %q NOT to be JSON", ct)
	}
}

func TestNewEntryIDFormatAndUniqueness(t *testing.T) {
	seen := map[EntryID]bool{}
	for i := 0; i < 100; i++ {
		id := NewEntryID()
		require.NotContains(t, seen, id, "entry IDs must be unique")
		seen[id] = true
		assert.Regexp(t, `^\d{14}-\d+$`, string(id), "expected YYYYMMDDHHMMSS-<counter> format")
	}
}

func TestHeadersToMapMultiValueAndRedaction(t *testing.T) {
	// Map literal on purpose: Set/Add canonicalize keys, but a literal keeps
	// lowercase/mixed-case keys so the case-insensitive redaction is tested
	// directly rather than relying on http.Header canonicalization.
	h := http.Header{
		"X-Multi":            {"one", "two"},
		"Authorization":      {"Bearer secret"},
		"cookie":             {"session=abc"},
		"CoOkIe":             {"mixed=case"},
		"Proxy-Authorization": {"Basic x"},
		"Set-Cookie":         {"a=b"},
	}

	m := headersToMap(h)

	assert.Equal(t, "one, two", m["X-Multi"], "multi-value headers should be joined")
	assert.Equal(t, "***REDACTED***", m["Authorization"])
	assert.Equal(t, "***REDACTED***", m["cookie"], "redaction must be case-insensitive")
	assert.Equal(t, "***REDACTED***", m["CoOkIe"], "redaction must be case-insensitive")
	assert.Equal(t, "***REDACTED***", m["Proxy-Authorization"])
	assert.Equal(t, "***REDACTED***", m["Set-Cookie"])
}

func TestCaptureResponseEmptyBody(t *testing.T) {
	header := http.Header{"Content-Type": []string{"application/json"}}
	cr := CaptureResponse(200, header, nil, time.Now())
	assert.Nil(t, cr.ResponseBody)
	assert.Equal(t, 200, cr.StatusCode)
}
