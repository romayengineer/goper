package output

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin down that any response body that parses as JSON is dumped to
// disk regardless of its Content-Type. A JSON payload served with a missing or
// non-JSON Content-Type used to be silently skipped by the writer even though
// the store captured it.
func TestJSONBodyWriterDumpsJSONBodyWithoutJSONContentType(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	body := `{"ok":true}`
	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:           capture.EntryID("plain"),
		Host:         "example.com",
		ContentType:  "text/plain",
		ResponseBody: &body,
	}))

	data, ok := fs.files[filepath.Join("out", "example.com", "plain.json")]
	require.True(t, ok, "a JSON body with a non-JSON Content-Type must still be dumped")
	assert.True(t, json.Valid(data))
}

func TestJSONBodyWriterDumpsJSONBodyWithEmptyContentType(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	body := `[1,2,3]`
	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:           capture.EntryID("noct"),
		Host:         "example.com",
		ResponseBody: &body,
	}))

	data, ok := fs.files[filepath.Join("out", "example.com", "noct.json")]
	require.True(t, ok, "a JSON body with no Content-Type must still be dumped")
	assert.True(t, json.Valid(data))
}

func TestNDJSONBodyWriterDumpsJSONBodyWithoutJSONContentType(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter("out", fs)

	body := `{"ok":true}`
	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:           capture.EntryID("plain"),
		Host:         "example.com",
		ContentType:  "text/plain",
		ResponseBody: &body,
	}))

	assert.Contains(t, fs.files, filepath.Join("out", "example.com", "responses.jsonl"))
}
