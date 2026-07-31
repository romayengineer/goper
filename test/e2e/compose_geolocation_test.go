//go:build e2e

// Package e2e — geolocation prompt suppression test.
//
// Brings up the compose stack (Xvfb overlay for headless/CI), drives Chrome to
// https://www.where-am-i.co/ and asserts that Chromium does NOT show the
// geolocation permission popup: the permission state is "denied" and a
// getCurrentPosition request resolves immediately instead of staying pending
// behind an open prompt.
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeolocationPromptBlocked(t *testing.T) {
	requireDocker(t)
	root := repoRoot(t)
	capturesDir := filepath.Join(root, "captures")

	require.NoError(t, os.RemoveAll(capturesDir))
	require.NoError(t, os.MkdirAll(capturesDir, 0o777))

	// 1. Bring up the stack (base + Xvfb overlay → headless-safe).
	upCtx, cancelUp := context.WithTimeout(context.Background(), 8*time.Minute)
	out, err := compose(upCtx, root, "up", "-d", "--build")
	cancelUp()
	require.NoError(t, err, "docker compose up --build failed:\n%s", out)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, _ = compose(ctx, root, "down", "-v")
	})

	// 2. goper API becomes healthy.
	waitFor(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := composeExec(ctx, root, "goper", "wget", "-qO-", "http://localhost:8081/api/stats")
		return err == nil && strings.Contains(out, "count")
	}, "goper API to become healthy")

	// 3. Chrome's CDP endpoint becomes reachable.
	waitFor(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := composeExec(ctx, root, "chrome", "node", "-e",
			"fetch('http://127.0.0.1:9222/json/version').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))")
		return err == nil
	}, "chrome CDP endpoint to become reachable")

	// 4. Probe: visit where-am-i.co and report the geolocation permission state.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	out, err = composeExec(ctx, root, "chrome", "node", "/app/geolocation-probe.js")
	cancel()
	require.NoError(t, err, "geolocation probe failed:\n%s", out)
	assert.Contains(t, out, "PERM_STATE:denied", "geolocation must be blocked by default")
	assert.Contains(t, out, "POPUP:absent", "no location permission popup may appear")
}
