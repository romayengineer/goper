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

func strPtr(s string) *string {
	return &s
}
