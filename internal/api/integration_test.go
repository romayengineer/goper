//go:build integration

package api

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunWithListenerServesEndpoints(t *testing.T) {
	server := NewServer(0, &mockStore{}, []byte("pem"))
	s := server.(*Server)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	done := make(chan error, 1)
	go func() { done <- s.RunWithListener(ln) }()

	base := "http://" + ln.Addr().String()
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get(base + "/api/stats")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp2, err := client.Get(base + "/api/requests")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	resp3, err := client.Get(base + "/api/ca.pem")
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusOK, resp3.StatusCode)
}
