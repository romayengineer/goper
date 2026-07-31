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

func (f *fakeFileSystem) Chmod(path string, perm os.FileMode) error {
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

func jsonEntry(host, id, body string) *capture.CapturedEntry {
	return &capture.CapturedEntry{
		ID:           capture.EntryID(id),
		Host:         host,
		ContentType:  "application/json",
		ResponseBody: &body,
	}
}

func TestJSONBodyWriterWritesPrettyFile(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "abc", `{"users":[{"id":1,"name":"alice"}]}`)))

	data, ok := fs.files[filepath.Join("out", "example.com", "abc.json")]
	require.True(t, ok, "expected file to be written to fake fs")
	assert.Equal(t, "{\n  \"users\": [\n    {\n      \"id\": 1,\n      \"name\": \"alice\"\n    }\n  ]\n}\n", string(data))
	assert.True(t, json.Valid(data), "file content should be valid JSON")
}

func TestJSONBodyWriterSeparatesDomains(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("api.one.com", "a", `{"i":1}`)))
	require.NoError(t, w.WriteEntry(jsonEntry("api.two.com", "b", `{"i":2}`)))

	assert.Contains(t, fs.files, filepath.Join("out", "api.one.com", "a.json"))
	assert.Contains(t, fs.files, filepath.Join("out", "api.two.com", "b.json"))
}

func TestJSONBodyWriterPreservesExactJSON(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	raw := `{"b":2,"a":[1,"x"],"z":{"nested":true}}`
	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "exact", raw)))

	data := fs.files[filepath.Join("out", "example.com", "exact.json")]
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
		ID:           capture.EntryID("html"),
		Host:         "example.com",
		ContentType:  "text/html",
		ResponseBody: &body,
	}))

	assert.Empty(t, fs.files, "no file should be created for non-JSON response")
}

func TestJSONBodyWriterSkipsNilBody(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:          capture.EntryID("nobody"),
		Host:        "example.com",
		ContentType: "application/json",
	}))

	assert.Empty(t, fs.files)
}

func TestJSONBodyWriterSkipsInvalidJSON(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "bad", `{"broken":`)))

	assert.Empty(t, fs.files, "invalid JSON should be skipped, not error")
}

func TestJSONBodyWriterCreatesDir(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("deep", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "n", `{"ok":true}`)))

	assert.True(t, fs.dirs[filepath.Join("deep", "example.com")], "expected MkdirAll to be called for the domain dir")
	assert.Contains(t, fs.files, filepath.Join("deep", "example.com", "n.json"))
}

func TestJSONBodyWriterMultipleFiles(t *testing.T) {
	fs := newFakeFS()
	w := newJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "one", `{"i":1}`)))
	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "two", `{"i":2}`)))

	assert.Contains(t, fs.files, filepath.Join("out", "example.com", "one.json"))
	assert.Contains(t, fs.files, filepath.Join("out", "example.com", "two.json"))
}

func TestJSONBodyWriterMkdirError(t *testing.T) {
	fs := newFakeFS()
	fs.mkdirErr = errors.New("permission denied")
	w := newJSONBodyWriter("out", fs)

	err := w.WriteEntry(jsonEntry("example.com", "a", `{"ok":true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestJSONBodyWriterWriteError(t *testing.T) {
	fs := newFakeFS()
	fs.writeErr = errors.New("disk full")
	w := newJSONBodyWriter("out", fs)

	err := w.WriteEntry(jsonEntry("example.com", "a", `{"ok":true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
}

func TestNDJSONBodyWriterAppendsLines(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "one", `{"a": 1}`)))
	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "two", `{"b": 2}`)))

	data := fs.files[filepath.Join("out", "example.com", "responses.jsonl")]
	lines := 0
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for dec.More() {
		var v interface{}
		require.NoError(t, dec.Decode(&v))
		lines++
	}
	assert.Equal(t, 2, lines, "expected 2 JSON records in NDJSON file")
}

func TestNDJSONBodyWriterSeparatesDomains(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("api.one.com", "a", `{"i":1}`)))
	require.NoError(t, w.WriteEntry(jsonEntry("api.two.com", "b", `{"i":2}`)))

	assert.Contains(t, fs.files, filepath.Join("out", "api.one.com", "responses.jsonl"))
	assert.Contains(t, fs.files, filepath.Join("out", "api.two.com", "responses.jsonl"))
}

func TestNDJSONBodyWriterCompactSingleLine(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter("out", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "one", "{\n  \"a\": 1,\n  \"b\": [1, 2, 3]\n}")))

	data := fs.files[filepath.Join("out", "example.com", "responses.jsonl")]
	assert.Equal(t, `{"a":1,"b":[1,2,3]}`+"\n", string(data))
}

func TestNDJSONBodyWriterSkipsNonJSON(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter("out", fs)

	body := "text"
	require.NoError(t, w.WriteEntry(&capture.CapturedEntry{
		ID:           capture.EntryID("x"),
		Host:         "example.com",
		ContentType:  "text/plain",
		ResponseBody: &body,
	}))

	assert.Empty(t, fs.files, "no file should be created when all entries are non-JSON")
}

func TestNDJSONBodyWriterCreatesDir(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter("deep", fs)

	require.NoError(t, w.WriteEntry(jsonEntry("example.com", "n", `{"ok":true}`)))

	assert.True(t, fs.dirs[filepath.Join("deep", "example.com")], "expected MkdirAll to be called for the domain dir")
	assert.Contains(t, fs.files, filepath.Join("deep", "example.com", "responses.jsonl"))
}

func TestNDJSONBodyWriterOpenError(t *testing.T) {
	fs := newFakeFS()
	fs.openErr = errors.New("too many open files")
	w := newNDJSONBodyWriter("out", fs)

	err := w.WriteEntry(jsonEntry("example.com", "a", `{"ok":true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too many open files")
}

func TestNDJSONBodyWriterConcurrent(t *testing.T) {
	fs := newFakeFS()
	w := newNDJSONBodyWriter("out", fs)

	const n = 100
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = w.WriteEntry(jsonEntry("example.com", "x", `{"i":1}`))
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}

	data := fs.files[filepath.Join("out", "example.com", "responses.jsonl")]
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Len(t, lines, n, "expected %d lines, no interleaving", n)
}

func TestSafeDomain(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"example.com", "example.com"},
		{"API.Example.com", "api.example.com"},
		{"localhost:8080", "localhost"},
		{"api.example.com:8443", "api.example.com"},
		{"exa mple.com", "exa_mple.com"},
		{"[::1]", "___1_"},
		{"..", "unknown"},
		{".", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, safeDomain(tc.host), "safeDomain(%q)", tc.host)
	}
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
