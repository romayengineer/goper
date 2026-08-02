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
	pretty, ok := indentJSON(entry)
	if !ok {
		return nil
	}

	domainDir, err := ensureOutputDir(w.fs, w.dir, entry.Host)
	if err != nil {
		return err
	}

	path := filepath.Join(domainDir, string(entry.ID)+".json")
	if err := w.fs.WriteFile(path, pretty, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// indentJSON pretty-prints the entry body; ok is false when the response is
// not valid JSON.
func indentJSON(entry *capture.CapturedEntry) ([]byte, bool) {
	body, ok := jsonBody(entry)
	if !ok {
		return nil, false
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		return nil, false // not valid JSON, skip
	}
	pretty.WriteByte('\n')

	return pretty.Bytes(), true
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
	line, ok := compactJSON(entry)
	if !ok {
		return nil
	}

	domainDir, err := ensureOutputDir(w.fs, w.dir, entry.Host)
	if err != nil {
		return err
	}

	return w.appendLine(filepath.Join(domainDir, "responses.jsonl"), line)
}

// appendLine appends a newline-terminated line to the per-domain JSONL file.
func (w *NDJSONBodyWriter) appendLine(path string, line []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := w.fs.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ensureOutputDir creates the per-domain output directory with permissive
// permissions so the host user (not just root, who runs goper) can manage the
// captures in the bind-mounted output dir. MkdirAll is subject to the process
// umask, so chmod explicitly.
func ensureOutputDir(fs FileSystem, dir, host string) (string, error) {
	domainDir := filepath.Join(dir, safeDomain(host))
	if err := fs.MkdirAll(domainDir, 0o777); err != nil {
		return "", fmt.Errorf("create output dir %s: %w", domainDir, err)
	}
	if err := fs.Chmod(domainDir, 0o777); err != nil {
		return "", fmt.Errorf("chmod output dir %s: %w", domainDir, err)
	}
	return domainDir, nil
}

// compactJSON compacts the entry body into a single newline-terminated line;
// ok is false when the response is not JSON.
func compactJSON(entry *capture.CapturedEntry) ([]byte, bool) {
	body, ok := jsonBody(entry)
	if !ok {
		return nil, false
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		return nil, false // not valid JSON, skip
	}
	compact.WriteByte('\n')

	return compact.Bytes(), true
}

// safeDomain derives a safe single-segment directory name from a request
// host: it strips any port, lowercases, and replaces characters that are
// unsafe in file paths. Host headers are attacker-controllable, so this also
// guards against path traversal (e.g. a ".." or "..." host). Falls back to
// "unknown".
func safeDomain(host string) string {
	name := sanitizeHost(splitHostname(host))
	if !validSegmentName(name) {
		return "unknown"
	}
	return name
}

// splitHostname strips a trailing port (e.g. "example.com:8080") and returns
// the host unchanged when there is no port.
func splitHostname(host string) string {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		return hostname
	}
	return host
}

// sanitizeHost lowercases a hostname and replaces characters that are unsafe
// in file paths with an underscore.
func sanitizeHost(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if isSafeRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func isSafeRune(r rune) bool {
	return isAlnum(r) || r == '.' || r == '-'
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// validSegmentName rejects empty or dot-segment names, which would escape the
// capture directory or be silently dropped by the filesystem.
func validSegmentName(name string) bool {
	return name != "" && name != "." && name != ".."
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
