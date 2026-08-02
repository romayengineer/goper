//go:build integration

package proxy

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The startProxy/proxyClient helpers and the HTTP/MITM round-trip tests live
// in server_test.go (untagged) so plain `go test` and `make cover` exercise
// the real serving path. This file keeps the readBodyBounded variants
// exercised under the integration tag.

func TestReadBodyBoundedNil(t *testing.T) {
	got, truncated, skipped, err := readBodyBounded(&http.Response{Body: nil}, 0)
	assert.NoError(t, err)
	assert.Nil(t, got)
	assert.False(t, truncated)
	assert.False(t, skipped)
}

func TestReadBodyBoundedEmpty(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(""))}
	got, truncated, skipped, err := readBodyBounded(resp, 0)
	assert.NoError(t, err)
	assert.Empty(t, got)
	assert.False(t, truncated)
	assert.False(t, skipped)

	rest, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Empty(t, rest)
}

func TestReadBodyBoundedContent(t *testing.T) {
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(`{"a":1}`))}
	got, truncated, skipped, err := readBodyBounded(resp, 0)
	assert.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(got))
	assert.False(t, truncated)
	assert.False(t, skipped)
}

func TestReadBodyBoundedTruncatesCaptureOnly(t *testing.T) {
	body := `{"a":1} and a long tail that must still reach the client`
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	got, truncated, skipped, err := readBodyBounded(resp, 5)
	assert.NoError(t, err)
	assert.Equal(t, 6, len(got), "captured at most limit+1 bytes")
	assert.True(t, truncated, "body longer than limit must be flagged truncated")
	assert.False(t, skipped)

	rest, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, body, string(rest), "full body must be re-served after capture")
}
