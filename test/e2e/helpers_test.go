//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const composeProject = "goper-e2e"

var composeFiles = []string{
	"-f", "docker-compose.yml",
	"-f", "docker-compose.integration.yml",
}

// repoRoot returns the project root (the directory containing go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to resolve test file path")

	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod not found)")
		}
		dir = parent
	}
}

// requireDocker skips the test when Docker is not available.
func requireDocker(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if out, err := runCmd(ctx, "", "docker", "info"); err != nil {
		t.Skipf("docker unavailable, skipping e2e: %v (%s)", err, out)
	}
	if out, err := runCmd(ctx, "", "docker", "compose", "version"); err != nil {
		t.Skipf("docker compose unavailable, skipping e2e: %v (%s)", err, out)
	}
}

func composeArgs(args ...string) []string {
	out := append([]string{"compose", "-p", composeProject}, composeFiles...)
	return append(out, args...)
}

// compose runs a docker compose command in the repo root.
func compose(ctx context.Context, root string, args ...string) (string, error) {
	return runCmd(ctx, root, "docker", composeArgs(args...)...)
}

// composeExec runs a command inside a running service container (no TTY).
func composeExec(ctx context.Context, root, service string, args ...string) (string, error) {
	return compose(ctx, root, append([]string{"exec", "-T", service}, args...)...)
}

// runCmd executes name with args in dir and returns combined output.
func runCmd(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// waitFor polls fn every second until it returns true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, fn func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, msg)
}

// countJSON returns the number of .json files in dir.
func countJSON(dir string) int {
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return 0
	}
	return len(matches)
}
