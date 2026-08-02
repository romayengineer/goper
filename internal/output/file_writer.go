package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/romayengineer/goper/internal/capture"
)

var (
	_ Writer     = (*JSONBodyWriter)(nil)
	_ Writer     = (*NDJSONBodyWriter)(nil)
	_ FileSystem = OSFileSystem{}
)

// FileSystem abstracts filesystem operations so the writers can be unit
// tested without performing real I/O.
type FileSystem interface {
	MkdirAll(path string, perm os.FileMode) error
	Chmod(path string, perm os.FileMode) error
	WriteFile(path string, data []byte, perm os.FileMode) error
	OpenFile(path string, flag int, perm os.FileMode) (io.WriteCloser, error)
}

// OSFileSystem is the real filesystem backed by the os package.
type OSFileSystem struct{}

func (OSFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OSFileSystem) Chmod(path string, perm os.FileMode) error {
	return os.Chmod(path, perm)
}

func (OSFileSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (OSFileSystem) OpenFile(path string, flag int, perm os.FileMode) (io.WriteCloser, error) {
	return os.OpenFile(path, flag, perm) // #nosec G304 -- path derives from the configured output dir plus a sanitized domain segment
}

// JSONBodyWriter writes each JSON response body to its own pretty-printed
// .json file named <entry-id>.json inside <dir>/<domain>. Non-JSON responses
// are skipped.
type JSONBodyWriter struct {
	dir string
	fs  FileSystem
}

func NewJSONBodyWriter(dir string) *JSONBodyWriter {
	return newJSONBodyWriter(dir, OSFileSystem{})
}

func newJSONBodyWriter(dir string, fs FileSystem) *JSONBodyWriter {
	return &JSONBodyWriter{dir: dir, fs: fs}
}

func (w *JSONBodyWriter) WriteEntry(entry *capture.CapturedEntry) error {
	body, ok := jsonBody(entry)
	if !ok {
		return nil
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		return nil // not valid JSON, skip
	}
	pretty.WriteByte('\n')

	domainDir := filepath.Join(w.dir, safeDomain(entry.Host))
	// 0o777 + chmod so the host user (not just root, who runs goper) can manage
	// the captures in the bind-mounted output dir. MkdirAll is subject to the
	// process umask, so chmod explicitly.
	if err := w.fs.MkdirAll(domainDir, 0o777); err != nil {
		return fmt.Errorf("create output dir %s: %w", domainDir, err)
	}
	if err := w.fs.Chmod(domainDir, 0o777); err != nil {
		return fmt.Errorf("chmod output dir %s: %w", domainDir, err)
	}

	path := filepath.Join(domainDir, string(entry.ID)+".json")
	if err := w.fs.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// NDJSONBodyWriter appends each JSON response body as a single compact
// JSON line to <dir>/<domain>/responses.jsonl. Non-JSON responses are skipped.
type NDJSONBodyWriter struct {
	mu  sync.Mutex
	dir string
	fs  FileSystem
}

func NewNDJSONBodyWriter(dir string) *NDJSONBodyWriter {
	return newNDJSONBodyWriter(dir, OSFileSystem{})
}

func newNDJSONBodyWriter(dir string, fs FileSystem) *NDJSONBodyWriter {
	return &NDJSONBodyWriter{dir: dir, fs: fs}
}

func (w *NDJSONBodyWriter) WriteEntry(entry *capture.CapturedEntry) error {
	body, ok := jsonBody(entry)
	if !ok {
		return nil
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		return nil // not valid JSON, skip
	}
	compact.WriteByte('\n')

	domainDir := filepath.Join(w.dir, safeDomain(entry.Host))
	// 0o777 + chmod so the host user (not just root, who runs goper) can manage
	// the captures in the bind-mounted output dir. MkdirAll is subject to the
	// process umask, so chmod explicitly.
	if err := w.fs.MkdirAll(domainDir, 0o777); err != nil {
		return fmt.Errorf("create output dir %s: %w", domainDir, err)
	}
	if err := w.fs.Chmod(domainDir, 0o777); err != nil {
		return fmt.Errorf("chmod output dir %s: %w", domainDir, err)
	}
	path := filepath.Join(domainDir, "responses.jsonl")

	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := w.fs.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(compact.Bytes()); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// safeDomain derives a safe single-segment directory name from a request
// host: it strips any port, lowercases, and replaces characters that are
// unsafe in file paths. Host headers are attacker-controllable, so this also
// guards against path traversal (e.g. a ".." or "..." host). Falls back to
// "unknown".
func safeDomain(host string) string {
	h := host
	if hostname, _, err := net.SplitHostPort(h); err == nil {
		h = hostname
	}

	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}

	name := b.String()
	if name == "" || name == "." || name == ".." {
		return "unknown"
	}
	return name
}

// jsonBody returns the raw response body bytes if the entry carries a JSON
// response, and ok=false otherwise.
func jsonBody(entry *capture.CapturedEntry) ([]byte, bool) {
	if entry == nil || entry.ResponseBody == nil {
		return nil, false
	}
	if !capture.IsJSONContentType(entry.ContentType) {
		return nil, false
	}
	return []byte(*entry.ResponseBody), true
}
