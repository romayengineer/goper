package output

import (
	"encoding/json"
	"io"

	"github.com/romayengineer/goper/internal/capture"
)

type Writer interface {
	WriteEntry(entry *capture.CapturedEntry) error
}

type JSONWriter struct {
	w io.Writer
}

func NewJSONWriter(w io.Writer) *JSONWriter {
	return &JSONWriter{w: w}
}

func (j *JSONWriter) WriteEntry(entry *capture.CapturedEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = j.w.Write(data)
	return err
}
