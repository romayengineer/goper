package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/romayengineer/goper/internal/capture"
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

	if err := w.WriteEntry(entry); err != nil {
		t.Fatalf("WriteEntry: %v", err)
	}

	var got capture.CapturedEntry
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got.ID != "abc" || got.Method != "GET" || got.StatusCode != 200 {
		t.Fatalf("entry round-trip mismatch: %+v", got)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Fatal("expected output to end with newline")
	}
}

func TestJSONWriterMultipleEntries(t *testing.T) {
	var buf bytes.Buffer
	w := NewJSONWriter(&buf)

	for i := 0; i < 3; i++ {
		entry := &capture.CapturedEntry{Method: "GET", URL: "http://example.com"}
		if err := w.WriteEntry(entry); err != nil {
			t.Fatalf("WriteEntry %d: %v", i, err)
		}
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

func TestJSONWriterIsWriter(t *testing.T) {
	var _ Writer = NewJSONWriter(&bytes.Buffer{})
}
