//go:build integration

package output

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONBodyWriterRealDisk(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONBodyWriter(dir)

	require.NoError(t, w.WriteEntry(jsonEntry("disk", `{"users":[{"id":1}]}`)))

	data, err := os.ReadFile(filepath.Join(dir, "disk.json"))
	require.NoError(t, err)
	assert.True(t, json.Valid(data))
	assert.Contains(t, string(data), "  \"users\": [")
}

func TestJSONBodyWriterRealDiskSkipped(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONBodyWriter(dir)

	body := "<html>x</html>"
	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:          capture.EntryID("html"),
		ContentType: "text/html",
		ResponseBody: &body,
	}))

	_, err := os.Stat(filepath.Join(dir, "html.json"))
	assert.True(t, os.IsNotExist(err), "non-JSON response should not create a file")
}

func TestNDJSONBodyWriterRealDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "out.jsonl")
	w := NewNDJSONBodyWriter(path)

	require.NoError(t, w.WriteEntry(jsonEntry("one", `{"a":1}`)))
	require.NoError(t, w.WriteEntry(jsonEntry("two", `{"b":2}`)))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Len(t, lines, 2)
}

func TestOSFileSystemAdapter(t *testing.T) {
	dir := t.TempDir()
	var fs FileSystem = OSFileSystem{}

	sub := filepath.Join(dir, "sub")
	require.NoError(t, fs.MkdirAll(sub, 0o755))

	fp := filepath.Join(sub, "f.txt")
	require.NoError(t, fs.WriteFile(fp, []byte("hello"), 0o644))

	f, err := fs.OpenFile(fp, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.Write([]byte(" world"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	data, err := os.ReadFile(fp)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(data))
}
