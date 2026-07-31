package capture

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCaptureRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/users?id=1", strings.NewReader(`{"name":"alice"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Custom", "one")

	entry := CaptureRequest(req)

	if entry.Method != http.MethodPost {
		t.Fatalf("method: got %q want POST", entry.Method)
	}
	if entry.URL != "https://example.com/api/users?id=1" {
		t.Fatalf("url: got %q", entry.URL)
	}
	if entry.Scheme != "https" {
		t.Fatalf("scheme: got %q want https", entry.Scheme)
	}
	if entry.Host != "example.com" {
		t.Fatalf("host: got %q want example.com", entry.Host)
	}
	if entry.Path != "/api/users" {
		t.Fatalf("path: got %q", entry.Path)
	}
	if entry.RequestBody == nil || *entry.RequestBody != `{"name":"alice"}` {
		t.Fatalf("body: got %v", entry.RequestBody)
	}

	if got := entry.RequestHeaders["Authorization"]; got != "***REDACTED***" {
		t.Fatalf("authorization should be redacted, got %q", got)
	}
	if got := entry.RequestHeaders["X-Custom"]; got != "one" {
		t.Fatalf("X-Custom: got %q", got)
	}

	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(restored) != `{"name":"alice"}` {
		t.Fatalf("body was not restored for re-read, got %q", string(restored))
	}
}

func TestCaptureRequestNoBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	entry := CaptureRequest(req)

	if entry.RequestBody == nil || *entry.RequestBody != "" {
		t.Fatalf("expected empty string body, got %v", entry.RequestBody)
	}
}

func TestCaptureResponseJSON(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Set-Cookie", "session=abc")

	start := time.Now()
	cr := CaptureResponse(200, header, []byte(`{"ok":true}`), start.Add(-5*time.Millisecond))

	if cr.StatusCode != 200 {
		t.Fatalf("status: got %d", cr.StatusCode)
	}
	if cr.ContentType != "application/json" {
		t.Fatalf("content type: got %q", cr.ContentType)
	}
	if cr.ResponseBody == nil || *cr.ResponseBody != `{"ok":true}` {
		t.Fatalf("body: got %v", cr.ResponseBody)
	}
	if got := cr.ResponseHeaders["Set-Cookie"]; got != "***REDACTED***" {
		t.Fatalf("set-cookie should be redacted, got %q", got)
	}
	if cr.DurationMs < 1 {
		t.Fatalf("duration: got %d, want >= 1", cr.DurationMs)
	}
}

func TestCaptureResponsePlainText(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "text/plain")

	cr := CaptureResponse(200, header, []byte("hello world"), time.Now())
	if cr.ResponseBody == nil || *cr.ResponseBody != "hello world" {
		t.Fatalf("expected printable body captured, got %v", cr.ResponseBody)
	}
}

func TestCaptureResponseLargeBody(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/json")

	big := make([]byte, 2*1024*1024)
	for i := range big {
		big[i] = 'x'
	}

	cr := CaptureResponse(200, header, big, time.Now())
	if cr.ResponseBody != nil {
		t.Fatalf("expected large body to be skipped")
	}
}

func TestCaptureResponseBinary(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/octet-stream")

	binary := []byte{0x00, 0x01, 0x02, 0xff, 0x00}
	cr := CaptureResponse(200, header, binary, time.Now())
	if cr.ResponseBody != nil {
		t.Fatalf("expected binary body to be skipped")
	}
}

func TestCombineEntry(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	reqEntry := CaptureRequest(req)

	result := CaptureResult{
		StatusCode:      201,
		ResponseHeaders: map[string]string{"Content-Type": "application/json"},
		ResponseBody:    strPtr(`{"created":true}`),
		ContentType:     "application/json",
		DurationMs:      42,
	}

	combined := CombineEntry(reqEntry, result)

	if combined.StatusCode != 201 {
		t.Fatalf("status: got %d", combined.StatusCode)
	}
	if combined.ResponseBody == nil || *combined.ResponseBody != `{"created":true}` {
		t.Fatalf("body: got %v", combined.ResponseBody)
	}
	if combined.DurationMs != 42 {
		t.Fatalf("duration: got %d", combined.DurationMs)
	}
	if combined.Method != http.MethodGet {
		t.Fatalf("method should carry over: got %q", combined.Method)
	}
	if combined.ID == "" {
		t.Fatal("expected combined entry to have an ID")
	}
}

func strPtr(s string) *string {
	return &s
}
