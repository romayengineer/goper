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
// the real serving path. This file keeps the readBody variants exercised
// under the integration tag.

func TestReadBodyNil(t *testing.T) {
	got, err := readBody(&http.Response{Body: nil})
	assert.NoError(t, err)
	assert.Nil(t, got)
}

func TestReadBodyEmpty(t *testing.T) {
	got, err := readBody(&http.Response{Body: io.NopCloser(strings.NewReader(""))})
	assert.NoError(t, err)
	assert.Empty(t, got)
}

func TestReadBodyContent(t *testing.T) {
	got, err := readBody(&http.Response{Body: io.NopCloser(strings.NewReader(`{"a":1}`))})
	assert.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(got))
}
