package iptables

import (
	"errors"
	"strings"
	"testing"
)

type mockRunner struct {
	calls      [][]string
	err        error
	rulesExist bool // if true, -C returns success
}

func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func (m *mockRunner) Run(name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	m.calls = append(m.calls, call)

	if m.err != nil {
		return nil, m.err
	}

	if containsFlag(call, "-C") {
		if m.rulesExist {
			return nil, nil
		}
		return nil, errors.New("rule does not exist")
	}

	return nil, nil
}

func (m *mockRunner) countFlag(flag string) int {
	count := 0
	for _, c := range m.calls {
		if containsFlag(c, flag) {
			count++
		}
	}
	return count
}

func TestNewManagerRules(t *testing.T) {
	m := NewManager(8080, &mockRunner{})

	if len(m.rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(m.rules))
	}

	wantPort80 := "-t nat -A OUTPUT -p tcp --dport 80 -j REDIRECT --to-port 8080"
	got := m.rules[1]
	args := append([]string{"-t", got.table, "-A", got.chain}, got.spec...)
	if strings.Join(args, " ") != wantPort80 {
		t.Fatalf("rule 2 mismatch:\n got %s\nwant %s", strings.Join(args, " "), wantPort80)
	}
}

func TestSetupAddsAllRules(t *testing.T) {
	runner := &mockRunner{}
	m := NewManager(8080, runner)

	if err := m.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if got := runner.countFlag("-A"); got != 3 {
		t.Fatalf("expected 3 -A calls, got %d", got)
	}
}

func TestSetupSkipsExistingRules(t *testing.T) {
	runner := &mockRunner{rulesExist: true}
	m := NewManager(8080, runner)

	if err := m.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if got := runner.countFlag("-C"); got != 3 {
		t.Fatalf("expected 3 -C existence checks, got %d", got)
	}
	if got := runner.countFlag("-A"); got != 0 {
		t.Fatalf("expected 0 -A calls when rules exist, got %d", got)
	}
}

func TestSetupError(t *testing.T) {
	runner := &mockRunner{err: errors.New("permission denied")}
	m := NewManager(8080, runner)

	if err := m.Setup(); err == nil {
		t.Fatal("expected Setup to return error")
	} else if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTeardownRemovesAllRules(t *testing.T) {
	runner := &mockRunner{}
	m := NewManager(8080, runner)

	if err := m.Teardown(); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	if got := runner.countFlag("-D"); got != 3 {
		t.Fatalf("expected 3 -D calls, got %d", got)
	}
}

func TestTeardownErrorDoesNotFail(t *testing.T) {
	runner := &mockRunner{err: errors.New("iptables failed")}
	m := NewManager(8080, runner)

	if err := m.Teardown(); err != nil {
		t.Fatalf("Teardown should swallow per-rule errors, got %v", err)
	}
}

func TestNewManagerNilRunnerDefaults(t *testing.T) {
	m := NewManager(8080, nil)
	if m.runner == nil {
		t.Fatal("expected default OSRunner when nil runner provided")
	}
}

func TestManagerImplementsRuleManager(t *testing.T) {
	var _ RuleManager = NewManager(8080, &mockRunner{})
}
