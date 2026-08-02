package capture

import (
	"strconv"
	"sync/atomic"
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

var idCounter atomic.Int64

func NewEntryID() EntryID {
	return EntryID(time.Now().Format("20060102150405") + "-" + strconv.Itoa(int(idCounter.Add(1))))
}
