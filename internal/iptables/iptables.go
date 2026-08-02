package iptables

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

type RuleManager interface {
	Setup() error
	Teardown() error
}

type CommandRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

type OSRunner struct{}

func (OSRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput() // #nosec G204 -- the rule specs are fixed in NewManager; name/args are not user-controlled
}

type Manager struct {
	proxyPort int
	rules     []rule
	runner    CommandRunner
}

type rule struct {
	table string
	chain string
	spec  []string
}

func NewManager(proxyPort int, runner CommandRunner) *Manager {
	if runner == nil {
		runner = OSRunner{}
	}

	goperUID := os.Getuid()

	return &Manager{
		proxyPort: proxyPort,
		runner:    runner,
		rules: []rule{
			{
				table: "nat",
				chain: "OUTPUT",
				spec:  []string{"-m", "owner", "--uid-owner", fmt.Sprintf("%d", goperUID), "-j", "RETURN"},
			},
			{
				table: "nat",
				chain: "OUTPUT",
				spec:  []string{"-p", "tcp", "--dport", "80", "-j", "REDIRECT", "--to-port", fmt.Sprintf("%d", proxyPort)},
			},
			{
				table: "nat",
				chain: "OUTPUT",
				spec:  []string{"-p", "tcp", "--dport", "443", "-j", "REDIRECT", "--to-port", fmt.Sprintf("%d", proxyPort)},
			},
		},
	}
}

func (m *Manager) Setup() error {
	slog.Info("installing iptables rules")

	for _, r := range m.rules {
		if err := m.ensureRule(r); err != nil {
			return err
		}
	}

	return nil
}

// ensureRule adds a rule unless it already exists.
func (m *Manager) ensureRule(r rule) error {
	exists, err := m.ruleExists(r)
	if err != nil {
		return fmt.Errorf("check rule existence: %w", err)
	}
	if exists {
		return nil
	}

	args := append([]string{"-t", r.table, "-A", r.chain}, r.spec...)
	out, err := m.runner.Run("iptables", args...)
	if err != nil {
		return fmt.Errorf("iptables %s: %s: %w", strings.Join(args, " "), string(out), err)
	}
	slog.Debug("rule added",
		"table", r.table,
		"chain", r.chain,
		"spec", strings.Join(r.spec, " "),
	)
	return nil
}

func (m *Manager) Teardown() error {
	slog.Info("removing iptables rules")

	for _, r := range m.rules {
		args := append([]string{"-t", r.table, "-D", r.chain}, r.spec...)
		out, err := m.runner.Run("iptables", args...)
		if err != nil {
			slog.Warn("remove rule failed",
				"rule", strings.Join(args, " "),
				"error", string(out),
			)
			continue
		}
	}

	return nil
}

func (m *Manager) ruleExists(r rule) (bool, error) {
	args := append([]string{"-t", r.table, "-C", r.chain}, r.spec...)
	out, err := m.runner.Run("iptables", args...)
	if err != nil {
		if isExecFailure(err) {
			return false, fmt.Errorf("iptables %s: %w", strings.Join(args, " "), err)
		}
		// Any other exit status means the rule is absent (iptables -C exits
		// non-zero when the rule does not exist).
		_ = out
		return false, nil
	}
	return true, nil
}

// isExecFailure reports whether a command error means the iptables binary
// could not run at all (as opposed to the rule simply not existing), so the
// caller surfaces a real failure instead of misreading it as "rule absent".
func isExecFailure(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr)
}
