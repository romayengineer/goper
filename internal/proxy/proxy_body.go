package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/romayengineer/goper/internal/capture"
)

// captureStreaming records a response whose body was left untouched because it
// is streaming (SSE etc.), storing the entry without a body.
func (s *Server) captureStreaming(data captureCtx, resp *http.Response) {
	result := s.recorder.CaptureResponse(resp.StatusCode, resp.Header, nil, data.start)
	fullEntry := s.recorder.CombineEntry(data.entry, result)
	s.store.Push(fullEntry)
	slog.Debug("request completed (streaming body, not captured)",
		"id", fullEntry.ID,
		"method", fullEntry.Method,
		"url", fullEntry.URL,
		"status", fullEntry.StatusCode,
	)
}

// captureResponse records a completed response into the store and fans it out
// to the configured outputs.
func (s *Server) captureResponse(data captureCtx, resp *http.Response, bodyBytes []byte) {
	result := s.recorder.CaptureResponse(resp.StatusCode, resp.Header, bodyBytes, data.start)
	fullEntry := s.recorder.CombineEntry(data.entry, result)
	s.store.Push(fullEntry)

	for _, w := range s.outputs {
		if err := w.WriteEntry(fullEntry); err != nil {
			slog.Error("output write failed", "error", err)
		}
	}

	slog.Debug("request completed",
		"id", fullEntry.ID,
		"method", fullEntry.Method,
		"url", fullEntry.URL,
		"status", fullEntry.StatusCode,
		"duration_ms", fullEntry.DurationMs,
	)
}

type captureCtx struct {
	entry capture.CapturedEntry
	start time.Time
}

// readBodyBounded reads the response body for capture, bounded to at most
// limit+1 bytes (limit <= 0 means unlimited). It then rewires resp.Body so the
// full body still streams to the client: the captured prefix is replayed
// followed by whatever remains in the original body.
//
// It returns:
//   - body: the captured prefix (never more than limit+1 bytes)
//   - truncated: true when the body exceeded the capture limit (the recorder
//     then drops the oversized body)
//   - skipped: true when the body was left untouched because it is a streaming
//     response (e.g. SSE) that must not be buffered — proxying would otherwise
//     stall until the limit filled or the stream ended
//   - err: a read failure; even on error resp.Body is rewired with whatever was
//     consumed so the client never loses bytes
func readBodyBounded(resp *http.Response, limit int64) (body []byte, truncated, skipped bool, err error) {
	if !canReadBody(resp) {
		return nil, false, resp.Body != nil, nil
	}

	buffered, err := readAndRewire(resp, limit)
	if err != nil {
		return nil, false, false, err
	}

	return buffered, captureExceededLimit(resp, buffered, limit), false, nil
}

// canReadBody reports whether the response body is present and not a streaming
// response that must be left untouched.
func canReadBody(resp *http.Response) bool {
	return resp.Body != nil && !isStreamingResponse(resp)
}

// readAndRewire reads the body for capture and rewires resp.Body so the full
// body still streams to the client: the captured prefix is replayed followed
// by whatever remains in the original body.
func readAndRewire(resp *http.Response, limit int64) ([]byte, error) {
	buffered, err := readForCapture(resp.Body, resp.ContentLength, limit)
	// Always rewire before returning: on error the consumed prefix must still
	// be delivered to the client followed by the untouched remainder.
	resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(buffered), resp.Body))
	if err != nil {
		return nil, err
	}
	return buffered, nil
}

// captureExceededLimit reports whether the captured prefix hit the capture
// limit, meaning the body was cut off. Unlimited capture still caps the buffer
// at unlimitedReadSafetyCap for chunked/close-delimited bodies.
func captureExceededLimit(resp *http.Response, buffered []byte, limit int64) bool {
	if limit > 0 {
		return int64(len(buffered)) == limit+1
	}
	return resp.ContentLength < 0 && int64(len(buffered)) == unlimitedReadSafetyCap
}

// readForCapture reads a body for capture: at most limit+1 bytes when a limit
// is configured (byte-bounded, so it always terminates), and for unlimited
// mode (limit <= 0) the full body when its length is known, else up to
// unlimitedReadSafetyCap so an endless stream cannot exhaust memory.
func readForCapture(body io.Reader, contentLength, limit int64) ([]byte, error) {
	if limit > 0 {
		return io.ReadAll(io.LimitReader(body, limit+1))
	}
	if contentLength >= 0 {
		return io.ReadAll(body)
	}
	return io.ReadAll(io.LimitReader(body, unlimitedReadSafetyCap))
}

// unlimitedReadSafetyCap bounds how much of an unknown-length (chunked) body
// is buffered when capture is unlimited (limit <= 0). Known-length bodies are
// still captured in full; this only guards against endless streams. A var
// (not const) so tests can shrink it.
var unlimitedReadSafetyCap int64 = 64 << 20 // 64 MiB

// isStreamingResponse reports whether a response carries a body that is
// intended to be consumed incrementally over time (SSE, MJPEG). Buffering such
// a body for capture would stall the client until the capture limit fills or
// the stream ends, so these responses are proxied without body capture.
func isStreamingResponse(resp *http.Response) bool {
	return resp.ContentLength < 0 && isStreamingContentType(resp.Header.Get("Content-Type"))
}

// isStreamingContentType reports whether a Content-Type denotes a
// server-streaming body. Parameters such as "; charset=utf-8" are ignored.
func isStreamingContentType(contentType string) bool {
	ct := capture.NormalizeMediaType(contentType)
	return ct == "text/event-stream" || ct == "multipart/x-mixed-replace"
}
