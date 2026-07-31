package iptables

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	assert.Len(t, m.rules, 3)

	wantPort80 := "-t nat -A OUTPUT -p tcp --dport 80 -j REDIRECT --to-port 8080"
	got := m.rules[1]
	args := append([]string{"-t", got.table, "-A", got.chain}, got.spec...)
	assert.Equal(t, wantPort80, strings.Join(args, " "))
}

func TestSetupAddsAllRules(t *testing.T) {
	runner := &mockRunner{}
	m := NewManager(8080, runner)

	require.NoError(t, m.Setup())
	assert.Equal(t, 3, runner.countFlag("-A"))
}

func TestSetupSkipsExistingRules(t *testing.T) {
	runner := &mockRunner{rulesExist: true}
	m := NewManager(8080, runner)

	require.NoError(t, m.Setup())

	assert.Equal(t, 3, runner.countFlag("-C"))
	assert.Equal(t, 0, runner.countFlag("-A"))
}

func TestSetupError(t *testing.T) {
	runner := &mockRunner{err: errors.New("permission denied")}
	m := NewManager(8080, runner)

	err := m.Setup()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func TestTeardownRemovesAllRules(t *testing.T) {
	runner := &mockRunner{}
	m := NewManager(8080, runner)

	require.NoError(t, m.Teardown())
	assert.Equal(t, 3, runner.countFlag("-D"))
}

func TestTeardownErrorDoesNotFail(t *testing.T) {
	runner := &mockRunner{err: errors.New("iptables failed")}
	m := NewManager(8080, runner)

	assert.NoError(t, m.Teardown(), "Teardown should swallow per-rule errors")
}

func TestNewManagerNilRunnerDefaults(t *testing.T) {
	m := NewManager(8080, nil)
	assert.NotNil(t, m.runner, "expected default OSRunner when nil runner provided")
}

func TestManagerImplementsRuleManager(t *testing.T) {
	var _ RuleManager = NewManager(8080, &mockRunner{})
}

func TestOSRunnerExecutesCommand(t *testing.T) {
	var runner CommandRunner = OSRunner{}

	out, err := runner.Run("echo", "-n", "hi")
	require.NoError(t, err)
	assert.Equal(t, "hi", string(out))
}

func TestOSRunnerReportsError(t *testing.T) {
	var runner CommandRunner = OSRunner{}

	_, err := runner.Run("false")
	require.Error(t, err)
}
