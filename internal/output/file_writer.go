package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/romayengineer/goper/internal/capture"
)

// JSONBodyWriter writes each JSON response body to its own pretty-printed
// .json file named <entry-id>.json inside the configured directory.
// Non-JSON responses are skipped.
type JSONBodyWriter struct {
	dir string
}

func NewJSONBodyWriter(dir string) *JSONBodyWriter {
	return &JSONBodyWriter{dir: dir}
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

	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return fmt.Errorf("create output dir %s: %w", w.dir, err)
	}

	path := filepath.Join(w.dir, string(entry.ID)+".json")
	if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// NDJSONBodyWriter appends each JSON response body as a single compact
// JSON line to the configured .jsonl file. Non-JSON responses are skipped.
type NDJSONBodyWriter struct {
	mu   sync.Mutex
	path string
}

func NewNDJSONBodyWriter(path string) *NDJSONBodyWriter {
	return &NDJSONBodyWriter{path: path}
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

	if dir := filepath.Dir(w.path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output dir %s: %w", dir, err)
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", w.path, err)
	}
	defer f.Close()

	if _, err := f.Write(compact.Bytes()); err != nil {
		return fmt.Errorf("write %s: %w", w.path, err)
	}
	return nil
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
