package capture

import (
	"time"
)

type EntryID string

type CapturedEntry struct {
	ID              EntryID           `json:"id"`
	Timestamp       time.Time         `json:"timestamp"`
	DurationMs      int64             `json:"duration_ms"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Scheme          string            `json:"scheme"`
	Host            string            `json:"host"`
	Path            string            `json:"path"`
	RequestHeaders  map[string]string `json:"request_headers"`
	RequestBody     *string           `json:"request_body"`
	StatusCode      int               `json:"status_code"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    *string           `json:"response_body"`
	ContentType     string            `json:"content_type"`
}

var idCounter int64

func NewEntryID() EntryID {
	idCounter++
	return EntryID(time.Now().Format("20060102150405") + "-" + itoa(int(idCounter)))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
