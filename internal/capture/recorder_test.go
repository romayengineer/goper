package capture

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRecorderCaptureRequest(t *testing.T) {
	rec := DefaultRecorder{}
	req := httptest.NewRequest(http.MethodPost, "https://example.com/api?q=1", strings.NewReader(`{"x":1}`))
	req.Header.Set("X-Test", "v")

	entry := rec.CaptureRequest(req)

	assert.Equal(t, http.MethodPost, entry.Method)
	assert.Equal(t, "https://example.com/api?q=1", entry.URL)
	assert.Equal(t, "v", entry.RequestHeaders["X-Test"])
	require.NotNil(t, entry.RequestBody)
	assert.Equal(t, `{"x":1}`, *entry.RequestBody)
}

func TestDefaultRecorderCaptureResponse(t *testing.T) {
	rec := DefaultRecorder{}
	header := http.Header{"Content-Type": []string{"application/json"}}

	start := time.Now()
	result := rec.CaptureResponse(http.StatusAccepted, header, []byte(`{"ok":true}`), start.Add(-3*time.Millisecond))

	assert.Equal(t, http.StatusAccepted, result.StatusCode)
	assert.Equal(t, "application/json", result.ContentType)
	require.NotNil(t, result.ResponseBody)
	assert.Equal(t, `{"ok":true}`, *result.ResponseBody)
	assert.GreaterOrEqual(t, result.DurationMs, int64(1))
}

func TestDefaultRecorderCombineEntry(t *testing.T) {
	rec := DefaultRecorder{}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	reqEntry := rec.CaptureRequest(req)

	result := CaptureResult{
		StatusCode:  200,
		ContentType: "application/json",
		DurationMs:  7,
	}

	combined := rec.CombineEntry(reqEntry, result)

	assert.Equal(t, http.StatusOK, combined.StatusCode)
	assert.Equal(t, "application/json", combined.ContentType)
	assert.Equal(t, int64(7), combined.DurationMs)
	assert.Equal(t, http.MethodGet, combined.Method)
	assert.NotEmpty(t, combined.ID)
}

func TestDefaultRecorderImplementsRecorder(t *testing.T) {
	var _ Recorder = DefaultRecorder{}
}
