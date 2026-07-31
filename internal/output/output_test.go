package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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

func TestJSONWriterIsWriter(t *testing.T) {
	var _ Writer = NewJSONWriter(&bytes.Buffer{})
}
