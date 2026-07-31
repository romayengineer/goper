//go:build e2e

// Package e2e tests the full docker-compose workflow:
//   goper (proxy) + windowed Chrome (headed, CDP) + Playwright driving it,
//   with JSON responses dumped into the host-mounted ./captures directory.
//
// Requires Docker (with the compose plugin) and internet access to the
// default target (https://httpbin.org/anything). Override the target with
// the E2E_TARGET_URL environment variable for offline environments.
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

func TestComposeChromeWorkflow(t *testing.T) {
	requireDocker(t)
	root := repoRoot(t)
	capturesDir := filepath.Join(root, "captures")

	// Deterministic baseline: start with an empty captures directory.
	require.NoError(t, os.RemoveAll(capturesDir))
	require.NoError(t, os.MkdirAll(capturesDir, 0o777))

	// 1. Bring up the stack (builds both images if needed).
	upCtx, cancelUp := context.WithTimeout(context.Background(), 8*time.Minute)
	out, err := compose(upCtx, root, "up", "-d", "--build")
	cancelUp()
	require.NoError(t, err, "docker compose up --build failed:\n%s", out)

	t.Cleanup(func() {
		downCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, _ = compose(downCtx, root, "down", "-v")
	})

	// 2. goper API becomes healthy.
	waitFor(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := composeExec(ctx, root, "goper", "wget", "-qO-", "http://localhost:8081/api/stats")
		return err == nil && strings.Contains(out, "count")
	}, "goper API to become healthy")

	// 3. Chrome's CDP endpoint (9222) becomes reachable.
	waitFor(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := composeExec(ctx, root, "chrome", "node", "-e",
			"fetch('http://127.0.0.1:9222/json/version').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))")
		return err == nil
	}, "chrome CDP endpoint to become reachable")

	// 4. goper generated its CA into the shared volume.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	out, err = composeExec(ctx, root, "goper", "sh", "-c", "ls /home/goper/.goper/ca/ca-cert.pem")
	cancel()
	require.NoError(t, err, "goper CA cert should exist in shared volume")
	assert.Contains(t, out, "ca-cert.pem")

	// 5. Baseline count of captures before Playwright runs.
	before := countJSON(capturesDir)

	// 6. Drive the windowed Chrome via Playwright through the goper proxy.
	// NOTE: the example closes the browser when done, which stops the
	// chrome container — so no chrome exec after this point.
	ctx, cancel = context.WithTimeout(context.Background(), 90*time.Second)
	out, err = composeExec(ctx, root, "chrome", "node", "/app/playwright-example.js")
	cancel()
	require.NoError(t, err, "playwright example failed:\n%s", out)

	// 7. A new pretty JSON file appears in the host-mounted captures dir.
	waitFor(t, 30*time.Second, func() bool {
		return countJSON(capturesDir) > before
	}, "a new capture to appear in ./captures")

	// 8. The goper API reports captured requests.
	waitFor(t, 30*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := composeExec(ctx, root, "goper", "wget", "-qO-", "http://localhost:8081/api/stats")
		return err == nil && strings.Contains(out, "count") && !strings.Contains(out, `"count": 0`)
	}, "goper API stats to report captured requests")
}
