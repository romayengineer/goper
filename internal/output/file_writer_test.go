package output

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/romayengineer/goper/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFileSystem is an in-memory FileSystem used to unit test the writers
// without performing any real I/O.
type fakeFileSystem struct {
	mu       sync.Mutex
	files    map[string][]byte
	dirs     map[string]bool
	mkdirErr error
	writeErr error
	openErr  error
}

func newFakeFS() *fakeFileSystem {
	return &fakeFileSystem{
		files: make(map[string][]byte),
		dirs:  make(map[string]bool),
	}
}

func (f *fakeFileSystem) MkdirAll(path string, perm os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mkdirErr != nil {
		return f.mkdirErr
	}
	f.dirs[path] = true
	return nil
}

func (f *fakeFileSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.files[path] = append([]byte(nil), data...)
	return nil
}

func (f *fakeFileSystem) OpenFile(path string, flag int, perm os.FileMode) (io.WriteCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return &fakeFile{fs: f, path: path}, nil
}

type fakeFile struct {
	fs   *fakeFileSystem
	path string
}

func (m *fakeFile) Write(p []byte) (int, error) {
	m.fs.mu.Lock()
	defer m.fs.mu.Unlock()
	m.fs.files[m.path] = append(m.fs.files[m.path], p...)
	return len(p), nil
}

func (m *fakeFile) Close() error { return nil }

func jsonEntry(id, body string) *capture.CapturedEntry {
	return &capture.CapturedEntry{
		ID:          capture.EntryID(id),
		ContentType: "application/json",
		ResponseBody: &body,
	}
}

func TestJSONBodyWriterWritesPrettyFile(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("abc", `{"users":[{"id":1,"name":"alice"}]}`)))

	data, ok := fs.files[filepath.Join("out", "abc.json")]
	require.True(t, ok, "expected file to be written to fake fs")
	assert.Equal(t, "{\n  \"users\": [\n    {\n      \"id\": 1,\n      \"name\": \"alice\"\n    }\n  ]\n}\n", string(data))
	assert.True(t, json.Valid(data), "file content should be valid JSON")
}

func TestJSONBodyWriterPreservesExactJSON(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	raw := `{"b":2,"a":[1,"x"],"z":{"nested":true}}`
	require.NoError(t, w.WriteEntry(jsonEntry("exact", raw)))

	data := fs.files[filepath.Join("out", "exact.json")]
	var got, want interface{}
	require.NoError(t, json.Unmarshal(data, &got))
	require.NoError(t, json.Unmarshal([]byte(raw), &want))
	assert.Equal(t, want, got, "content should round-trip exactly")
}

func TestJSONBodyWriterSkipsNonJSON(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	body := "<html>not json</html>"
	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:          capture.EntryID("html"),
		ContentType: "text/html",
		ResponseBody: &body,
	}))

	assert.Empty(t, fs.files, "no file should be created for non-JSON response")
}

func TestJSONBodyWriterSkipsNilBody(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:          capture.EntryID("nobody"),
		ContentType: "application/json",
	}))

	assert.Empty(t, fs.files)
}

func TestJSONBodyWriterSkipsInvalidJSON(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("bad", `{"broken":`)))

	assert.Empty(t, fs.files, "invalid JSON should be skipped, not error")
}

func TestJSONBodyWriterCreatesDir(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("deep/nested", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("n", `{"ok":true}`)))

	assert.True(t, fs.dirs["deep/nested"], "expected MkdirAll to be called for the output dir")
	assert.Contains(t, fs.files, filepath.Join("deep", "nested", "n.json"))
}

func TestJSONBodyWriterMultipleFiles(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("one", `{"i":1}`)))
	require.NoError(t, w.WriteEntry(jsonEntry("two", `{"i":2}`)))

	assert.Contains(t, fs.files, filepath.Join("out", "one.json"))
	assert.Contains(t, fs.files, filepath.Join("out", "two.json"))
}

func TestJSONBodyWriterMkdirError(t *testing.T) {
	fs := newFakeFS()
	fs.mkdirErr = errors.New("permission denied")
	w := newJSONBodyWriter("out", fs)

	err := w.WriteEntry(jsonEntry("a", `{"ok":true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestJSONBodyWriterWriteError(t *testing.T) {
	fs := newFakeFS()
	fs.writeErr = errors.New("disk full")
	w := newJSONBodyWriter("out", fs)

	err := w.WriteEntry(jsonEntry("a", `{"ok":true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
}

func TestNDJSONBodyWriterAppendsLines(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter(filepath.Join("out", "responses.jsonl"), fs)

	require.NoError(t, w.WriteEntry(jsonEntry("one", `{"a": 1}`)))
	require.NoError(t, w.WriteEntry(jsonEntry("two", `{"b": 2}`)))

	data := fs.files[filepath.Join("out", "responses.jsonl")]
	lines := 0
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for dec.More() {
		var v interface{}
		require.NoError(t, dec.Decode(&v))
		lines++
	}
	assert.Equal(t, 2, lines, "expected 2 JSON records in NDJSON file")
}

func TestNDJSONBodyWriterCompactSingleLine(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter(filepath.Join("out", "out.jsonl"), fs)

	require.NoError(t, w.WriteEntry(jsonEntry("one", "{\n  \"a\": 1,\n  \"b\": [1, 2, 3]\n}")))

	data := fs.files[filepath.Join("out", "out.jsonl")]
	assert.Equal(t, `{"a":1,"b":[1,2,3]}`+"\n", string(data))
}

func TestNDJSONBodyWriterSkipsNonJSON(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter(filepath.Join("out", "out.jsonl"), fs)

	body := "text"
	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:          capture.EntryID("x"),
		ContentType: "text/plain",
		ResponseBody: &body,
	}))

	assert.Empty(t, fs.files, "no file should be created when all entries are non-JSON")
}

func TestNDJSONBodyWriterCreatesDir(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter(filepath.Join("deep", "nested", "out.jsonl"), fs)

	require.NoError(t, w.WriteEntry(jsonEntry("n", `{"ok":true}`)))

	assert.True(t, fs.dirs["deep/nested"], "expected MkdirAll to be called for parent dir")
	assert.Contains(t, fs.files, filepath.Join("deep", "nested", "out.jsonl"))
}

func TestNDJSONBodyWriterOpenError(t *testing.T) {
	fs := newFakeFS()
	fs.openErr = errors.New("too many open files")
	w := newNDJSONBodyWriter(filepath.Join("out", "out.jsonl"), fs)

	err := w.WriteEntry(jsonEntry("a", `{"ok":true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many open files")
}

func TestNDJSONBodyWriterConcurrent(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter(filepath.Join("out", "out.jsonl"), fs)

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

	data := fs.files[filepath.Join("out", "out.jsonl")]
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Len(t, lines, n, "expected %d lines, no interleaving", n)
}

func TestJSONBodyWriterImplementsWriter(t *testing.T) {
	var _ Writer = (*JSONBodyWriter)(nil)
}

func TestNDJSONBodyWriterImplementsWriter(t *testing.T) {
	var _ Writer = (*NDJSONBodyWriter)(nil)
}

func TestOSFileSystemImplementsFileSystem(t *testing.T) {
	var _ FileSystem = OSFileSystem{}
}
