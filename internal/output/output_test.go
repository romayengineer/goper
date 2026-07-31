package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONWriterWriteEntry(t *testing.T) {
	var buf bytes.Buffer
	w := NewJSONWriter(&buf)

	body := `{"users":[]}`
	entry := &capture.CapturedEntry{
		ID:              "abc",
		Method:          "GET",
		URL:             "http://example.com/api",
		StatusCode:      200,
		ContentType:     "application/json",
		ResponseBody:    &body,
		ResponseHeaders: map[string]string{"Content-Type": "application/json"},
	}

	require.NoError(t, w.WriteEntry(entry))

	var got capture.CapturedEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got), "output should be valid JSON")
	assert.Equal(t, "abc", string(got.ID))
	assert.Equal(t, "GET", got.Method)
	assert.Equal(t, 200, got.StatusCode)
	assert.True(t, strings.HasSuffix(buf.String(), "\n"), "expected output to end with newline")
}

func TestJSONWriterMultipleEntries(t *testing.T) {
	var buf bytes.Buffer
	w := NewJSONWriter(&buf)

	for i := 0; i < 3; i++ {
		entry := &capture.CapturedEntry{Method: "GET", URL: "http://example.com"}
		require.NoError(t, w.WriteEntry(entry))
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Len(t, lines, 3)
}

func TestJSONWriterAllFields(t *testing.T) {
	var buf bytes.Buffer
	w := NewJSONWriter(&buf)

	ts := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	reqBody := `{"q":"x"}`
	respBody := `{"r":1}`
	entry := &capture.CapturedEntry{
		ID:         "full",
		Timestamp:  ts,
		DurationMs: 123,
		Method:     "POST",
		URL:        "https://api.example.com/v1?q=x",
		Scheme:     "https",
		Host:       "api.example.com",
		Path:       "/v1",
		RequestHeaders: map[string]string{
			"Accept": "application/json",
		},
		RequestBody: &reqBody,
		StatusCode:  201,
		ResponseHeaders: map[string]string{
			"Content-Type": "application/json",
		},
		ResponseBody: &respBody,
		ContentType:  "application/json",
	}

	require.NoError(t, w.WriteEntry(entry))

	var got capture.CapturedEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))

	assert.Equal(t, entry.ID, got.ID)
	assert.Equal(t, ts, got.Timestamp)
	assert.Equal(t, int64(123), got.DurationMs)
	assert.Equal(t, "POST", got.Method)
	assert.Equal(t, "https://api.example.com/v1?q=x", got.URL)
	assert.Equal(t, "https", got.Scheme)
	assert.Equal(t, "api.example.com", got.Host)
	assert.Equal(t, "/v1", got.Path)
	assert.Equal(t, "application/json", got.RequestHeaders["Accept"])
	require.NotNil(t, got.RequestBody)
	assert.Equal(t, reqBody, *got.RequestBody)
	assert.Equal(t, 201, got.StatusCode)
	assert.Equal(t, "application/json", got.ResponseHeaders["Content-Type"])
	require.NotNil(t, got.ResponseBody)
	assert.Equal(t, respBody, *got.ResponseBody)
	assert.Equal(t, "application/json", got.ContentType)
}

func TestJSONWriterNilPointers(t *testing.T) {
	var buf bytes.Buffer
	w := NewJSONWriter(&buf)

	entry := &capture.CapturedEntry{Method: "GET", URL: "http://example.com"}
	require.NoError(t, w.WriteEntry(entry))

	var got capture.CapturedEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Nil(t, got.RequestBody)
	assert.Nil(t, got.ResponseBody)
}

func TestJSONWriterIsWriter(t *testing.T) {
	var _ Writer = NewJSONWriter(&bytes.Buffer{})
}
