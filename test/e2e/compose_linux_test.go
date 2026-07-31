//go:build e2e && linux

// Package e2e — Linux window test.
//
// Brings up the docker-compose stack using the BASE compose file only (no
// Xvfb overlay), so Chromium opens a real window on the local Linux X display
// (via /tmp/.X11-unix + DISPLAY). Verifies that a window actually exists via
// xwininfo, then drives it with Playwright through the goper proxy and asserts
// a JSON capture is dumped to ./captures.
//
// Requires: Docker + a running X display. Skips with instructions if the
// display is unavailable. Run `make setup-linux` once per session.
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const linuxComposeProject = "goper-e2e-linux"

func composeLinuxArgs(args ...string) []string {
	return composeProjectArgs(linuxComposeProject, nil, args...)
}

func composeLinux(ctx context.Context, root string, args ...string) (string, error) {
	return runCmd(ctx, root, "docker", composeLinuxArgs(args...)...)
}

func composeLinuxExec(ctx context.Context, root, service string, args ...string) (string, error) {
	return composeLinux(ctx, root, append([]string{"exec", "-T", service}, args...)...)
}

// requireLinuxX11 verifies that a local X display is reachable by the
// container and skips with actionable setup instructions otherwise.
func requireLinuxX11(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux with an X display")
	}
	if os.Getenv("DISPLAY") == "" {
		t.Skip("DISPLAY is not set — run this from a graphical session")
	}

	// Allow the container (local unix socket) to connect. Access control
	// re-enables between sessions, so force it right before the stack starts
	// rather than relying on a stale state.
	if out, err := runCmd(context.Background(), "", "xhost", "+local:"); err != nil {
		t.Skipf("cannot reach the X display (%v: %s) — allow Docker: xhost +local:", err, out)
	}

	out, err := runCmd(context.Background(), "", "xhost")
	if err != nil {
		t.Skipf("cannot query the X display (%v: %s) — run: xhost +local:", err, out)
	}
	if !strings.Contains(out, "LOCAL") && !strings.Contains(out, "access control disabled") {
		t.Skip("X access control is still enabled after `xhost +local:` — run: xhost +local:")
	}
}

func TestComposeChromeWorkflowLinuxWindow(t *testing.T) {
	requireDocker(t)
	requireLinuxX11(t)

	root := repoRoot(t)
	capturesDir := filepath.Join(root, "captures")

	require.NoError(t, os.RemoveAll(capturesDir))
	require.NoError(t, os.MkdirAll(capturesDir, 0o777))

	// 1. Bring up the stack with the BASE compose file (real X display).
	upCtx, cancelUp := context.WithTimeout(context.Background(), 8*time.Minute)
	out, err := composeLinux(upCtx, root, "up", "-d", "--build")
	cancelUp()
	require.NoError(t, err, "docker compose up --build failed:\n%s", out)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, _ = composeLinux(ctx, root, "down", "-v")
	})

	// 2. goper API becomes healthy.
	waitFor(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := composeLinuxExec(ctx, root, "goper", "wget", "-qO-", "http://localhost:8081/api/stats")
		return err == nil && strings.Contains(out, "count")
	}, "goper API to become healthy")

	// 3. Chrome CDP reachable. Chromium exits if it cannot connect to the X
	//    server, so this also proves the windowed browser is up.
	waitFor(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := composeLinuxExec(ctx, root, "chrome", "node", "-e",
			"fetch('http://127.0.0.1:9222/json/version').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))")
		return err == nil
	}, "chrome CDP endpoint to become reachable")

	// 4. WINDOW ASSERTION: a Chromium window must exist on the X display.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	out, err = composeLinuxExec(ctx, root, "chrome", "xwininfo", "-root", "-children")
	cancel()
	require.NoError(t, err, "xwininfo should query the X display from the container")
	assert.Contains(t, out, "Chromium", "expected a Chromium window on the X display")

	// 5. goper generated its CA into the shared volume.
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	out, err = composeLinuxExec(ctx, root, "goper", "sh", "-c", "ls /home/goper/.goper/ca/ca-cert.pem")
	cancel()
	require.NoError(t, err, "goper CA cert should exist in shared volume")
	assert.Contains(t, out, "ca-cert.pem")

	// 6. Baseline capture count.
	before := countJSON(capturesDir)

	// 7. Drive the actual windowed Chrome via Playwright through goper.
	// NOTE: the example closes the browser when done, so no chrome exec
	// after this point.
	ctx, cancel = context.WithTimeout(context.Background(), 90*time.Second)
	out, err = composeLinuxExec(ctx, root, "chrome", "node", "/app/playwright-example.js")
	cancel()
	require.NoError(t, err, "playwright example failed:\n%s", out)

	// 8. A new pretty JSON file appears in the host-mounted captures dir.
	waitFor(t, 30*time.Second, func() bool {
		return countJSON(capturesDir) > before
	}, "a new capture to appear in ./captures")

	// 9. The goper API reports captured requests.
	waitFor(t, 30*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := composeLinuxExec(ctx, root, "goper", "wget", "-qO-", "http://localhost:8081/api/stats")
		return err == nil && strings.Contains(out, "count") && !strings.Contains(out, `"count": 0`)
	}, "goper API stats to report captured requests")
}
