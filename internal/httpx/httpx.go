// Package httpx provides the shared HTTP server setup used by goper's API and
// proxy servers.
package httpx

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// Serve serves handler on l with the standard goper server timeouts. A write
// timeout is intentionally omitted so long-running streams and large downloads
// are never killed mid-transfer; ReadHeaderTimeout alone mitigates slow-loris
// style connection exhaustion. A readTimeout <= 0 disables the full read
// timeout.
func Serve(handler http.Handler, l net.Listener, readTimeout time.Duration) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       readTimeout,
		IdleTimeout:       120 * time.Second,
	}
	return srv.Serve(l)
}

// Listen opens a TCP listener on a port.
func Listen(port int) (net.Listener, error) {
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return ln, nil
}
