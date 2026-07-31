//go:build e2e && darwin

// Package e2e — macOS window test.
//
// Brings up the docker-compose stack using the BASE compose file only (no
// Xvfb overlay), so Chromium opens a real window on the Mac through XQuartz
// (DISPLAY=host.docker.internal:0). Verifies that a window actually exists on
// the display via xwininfo, then drives it with Playwright through the goper
// proxy and asserts a JSON capture is dumped to ./captures.
//
// Requires: Docker + XQuartz (see `make setup-mac`). Skips with instructions
// if the display is not available.
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

const macComposeProject = "goper-e2e-mac"

func composeMacArgs(args ...string) []string {
	return composeProjectArgs(macComposeProject, nil, args...)
}

func composeMac(ctx context.Context, root string, args ...string) (string, error) {
	return runCmd(ctx, root, "docker", composeMacArgs(args...)...)
}

func composeMacExec(ctx context.Context, root, service string, args ...string) (string, error) {
	return composeMac(ctx, root, append([]string{"exec", "-T", service}, args...)...)
}

// requireMacX11 verifies that an XQuartz display is available and skips the
// test with actionable setup instructions otherwise.
func requireMacX11(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS with XQuartz")
	}

	// 1. XQuartz process running (binary is X11.bin; launcher may show as XQuartz).
	running := false
	for _, name := range []string{"X11.bin", "XQuartz"} {
		if out, _ := runCmd(context.Background(), "", "pgrep", "-x", name); strings.TrimSpace(out) != "" {
			running = true
			break
		}
	}
	if !running {
		t.Skip("XQuartz is not running — start it with: open -a XQuartz")
	}

	// 2. TCP listening enabled so the Docker VM can reach the display.
	tcpOK := false
	for _, domain := range []string{"org.xquartz.X11", "org.macosforge.xquartz.X11"} {
		if out, err := runCmd(context.Background(), "", "defaults", "read", domain, "nolisten_tcp"); err == nil && strings.TrimSpace(out) == "0" {
			tcpOK = true
			break
		}
	}
	if !tcpOK {
		t.Skip("XQuartz TCP is disabled — run: defaults write org.xquartz.X11 nolisten_tcp -bool false")
	}

	// 3. Display reachable with access allowed. XQuartz re-enables access
	//    control between sessions, so force `xhost +` here — right before the
	//    compose stack starts — rather than relying on a stale state.
	if out, err := runCmd(context.Background(), "", "sh", "-c", "DISPLAY=:0 /opt/X11/bin/xhost +"); err != nil {
		t.Skipf("cannot reach the X display (%v) — allow Docker: /opt/X11/bin/xhost +", err)
		_ = out
	}

	out, err := runCmd(context.Background(), "", "sh", "-c", "DISPLAY=:0 /opt/X11/bin/xhost")
	if err != nil || !strings.Contains(out, "access control disabled") {
		t.Skip("X access control is still enabled after `xhost +` — run: /opt/X11/bin/xhost +")
	}
}

func TestComposeChromeWorkflowMacWindow(t *testing.T) {
	requireDocker(t)
	requireMacX11(t)

	root := repoRoot(t)
	capturesDir := filepath.Join(root, "captures")

	require.NoError(t, os.RemoveAll(capturesDir))
	require.NoError(t, os.MkdirAll(capturesDir, 0o777))

	// 1. Bring up the stack with the BASE compose file (real XQuartz display).
	upCtx, cancelUp := context.WithTimeout(context.Background(), 8*time.Minute)
	out, err := composeMac(upCtx, root, "up", "-d", "--build")
	cancelUp()
	require.NoError(t, err, "docker compose up --build failed:\n%s", out)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, _ = composeMac(ctx, root, "down", "-v")
	})

	// 2. goper API becomes healthy.
	waitFor(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := composeMacExec(ctx, root, "goper", "wget", "-qO-", "http://localhost:8081/api/stats")
		return err == nil && strings.Contains(out, "count")
	}, "goper API to become healthy")

	// 3. Chrome CDP reachable. Chromium exits if it cannot connect to the X
	//    server, so this also proves the windowed browser is up.
	waitFor(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := composeMacExec(ctx, root, "chrome", "node", "-e",
			"fetch('http://127.0.0.1:9222/json/version').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))")
		return err == nil
	}, "chrome CDP endpoint to become reachable")

	// 4. WINDOW ASSERTION: a Chromium window must exist on the macOS display.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	out, err = composeMacExec(ctx, root, "chrome", "xwininfo", "-root", "-children")
	cancel()
	require.NoError(t, err, "xwininfo should query the macOS display from the container")
	assert.Contains(t, out, "Chromium", "expected a Chromium window on the X display")

	// 5. goper generated its CA into the shared volume.
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	out, err = composeMacExec(ctx, root, "goper", "sh", "-c", "ls /home/goper/.goper/ca/ca-cert.pem")
	cancel()
	require.NoError(t, err, "goper CA cert should exist in shared volume")
	assert.Contains(t, out, "ca-cert.pem")

	// 6. Baseline capture count.
	before := countJSON(capturesDir)

	// 7. Drive the actual windowed Chrome via Playwright through goper.
	// NOTE: the example closes the browser when done, stopping the chrome
	// container — no chrome exec after this point.
	ctx, cancel = context.WithTimeout(context.Background(), 90*time.Second)
	out, err = composeMacExec(ctx, root, "chrome", "node", "/app/playwright-example.js")
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
		out, err := composeMacExec(ctx, root, "goper", "wget", "-qO-", "http://localhost:8081/api/stats")
		return err == nil && strings.Contains(out, "count") && !strings.Contains(out, `"count": 0`)
	}, "goper API stats to report captured requests")
}
