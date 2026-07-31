package output

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func jsonEntry(id, body string) *capture.CapturedEntry {
	return &capture.CapturedEntry{
		ID:          capture.EntryID(id),
		ContentType: "application/json",
		ResponseBody: &body,
	}
}

func TestJSONBodyWriterWritesPrettyFile(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONBodyWriter(dir)

	require.NoError(t, w.WriteEntry(jsonEntry("abc", `{"users":[{"id":1,"name":"alice"}]}`)))

	path := filepath.Join(dir, "abc.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	assert.Equal(t, "{\n  \"users\": [\n    {\n      \"id\": 1,\n      \"name\": \"alice\"\n    }\n  ]\n}\n", string(data))
	assert.True(t, json.Valid(data), "file content should be valid JSON")
}

func TestJSONBodyWriterPreservesExactJSON(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONBodyWriter(dir)

	raw := `{"b":2,"a":[1,"x"],"z":{"nested":true}}`
	require.NoError(t, w.WriteEntry(jsonEntry("exact", raw)))

	data, err := os.ReadFile(filepath.Join(dir, "exact.json"))
	require.NoError(t, err)

	var got, want interface{}
	require.NoError(t, json.Unmarshal(data, &got))
	require.NoError(t, json.Unmarshal([]byte(raw), &want))
	assert.Equal(t, want, got, "content should round-trip exactly")
}

func TestJSONBodyWriterSkipsNonJSON(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONBodyWriter(dir)

	body := "<html>not json</html>"
	entry := &capture.CapturedEntry{
		ID:          capture.EntryID("html"),
		ContentType: "text/html",
		ResponseBody: &body,
	}

	require.NoError(t, w.WriteEntry(entry))
	_, err := os.Stat(filepath.Join(dir, "html.json"))
	assert.True(t, os.IsNotExist(err), "no file should be created for non-JSON response")
}

func TestJSONBodyWriterSkipsNilBody(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONBodyWriter(dir)

	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:          capture.EntryID("nobody"),
		ContentType: "application/json",
	}))

	_, err := os.Stat(filepath.Join(dir, "nobody.json"))
	assert.True(t, os.IsNotExist(err))
}

func TestJSONBodyWriterSkipsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONBodyWriter(dir)

	require.NoError(t, w.WriteEntry(jsonEntry("bad", `{"broken":`)))

	_, err := os.Stat(filepath.Join(dir, "bad.json"))
	assert.True(t, os.IsNotExist(err), "invalid JSON should be skipped, not error")
}

func TestJSONBodyWriterCreatesNestedDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "deep", "nested")
	w := NewJSONBodyWriter(dir)

	require.NoError(t, w.WriteEntry(jsonEntry("n", `{"ok":true}`)))
	data, err := os.ReadFile(filepath.Join(dir, "n.json"))
	require.NoError(t, err)
	assert.True(t, json.Valid(data))
}

func TestJSONBodyWriterMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONBodyWriter(dir)

	require.NoError(t, w.WriteEntry(jsonEntry("one", `{"i":1}`)))
	require.NoError(t, w.WriteEntry(jsonEntry("two", `{"i":2}`)))

	assert.FileExists(t, filepath.Join(dir, "one.json"))
	assert.FileExists(t, filepath.Join(dir, "two.json"))
}

func TestNDJSONBodyWriterAppendsLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "responses.jsonl")
	w := NewNDJSONBodyWriter(path)

	require.NoError(t, w.WriteEntry(jsonEntry("one", `{"a": 1}`)))
	require.NoError(t, w.WriteEntry(jsonEntry("two", `{"b": 2}`)))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	lines := 0
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var v interface{}
		require.NoError(t, dec.Decode(&v))
		lines++
	}
	assert.Equal(t, 2, lines, "expected 2 JSON records in NDJSON file")
}

func TestNDJSONBodyWriterCompactSingleLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	w := NewNDJSONBodyWriter(path)

	require.NoError(t, w.WriteEntry(jsonEntry("one", "{\n  \"a\": 1,\n  \"b\": [1, 2, 3]\n}")))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"b":[1,2,3]}`+"\n", string(data))
}

func TestNDJSONBodyWriterSkipsNonJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	w := NewNDJSONBodyWriter(path)

	body := "text"
	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:          capture.EntryID("x"),
		ContentType: "text/plain",
		ResponseBody: &body,
	}))

	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "no file should be created when all entries are non-JSON")
}

func TestNDJSONBodyWriterCreatesNestedDir(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "deep", "nested", "out.jsonl")
	w := NewNDJSONBodyWriter(path)

	require.NoError(t, w.WriteEntry(jsonEntry("n", `{"ok":true}`)))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`+"\n", string(data))
}

func TestNDJSONBodyWriterConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jsonl")
	w := NewNDJSONBodyWriter(path)

	const n = 100
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = w.WriteEntry(jsonEntry("x", `{"i":1}`))
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Len(t, lines, n, "expected %d lines, no interleaving", n)
}
