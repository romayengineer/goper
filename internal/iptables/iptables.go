package iptables

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

type Manager struct {
	proxyPort int
	rules     []rule
}

type rule struct {
	table string
	chain string
	spec  []string
}

func NewManager(proxyPort int) *Manager {
	goperUID := 1000

	return &Manager{
		proxyPort: proxyPort,
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
	log.Println("installing iptables rules...")

	for _, r := range m.rules {
		exists, err := m.ruleExists(r)
		if err != nil {
			return fmt.Errorf("check rule existence: %w", err)
		}
		if exists {
			continue
		}

		args := append([]string{"-t", r.table, "-A", r.chain}, r.spec...)
		cmd := exec.Command("iptables", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("iptables %s: %s: %w", strings.Join(args, " "), string(out), err)
		}
		log.Printf("  added: iptables -t %s -A %s %s", r.table, r.chain, strings.Join(r.spec, " "))
	}

	return nil
}

func (m *Manager) Teardown() error {
	log.Println("removing iptables rules...")

	for _, r := range m.rules {
		args := append([]string{"-t", r.table, "-D", r.chain}, r.spec...)
		cmd := exec.Command("iptables", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("warning: iptables -D %s: %s", strings.Join(args, " "), string(out))
			continue
		}
	}

	return nil
}

func (m *Manager) ruleExists(r rule) (bool, error) {
	args := append([]string{"-t", r.table, "-C", r.chain}, r.spec...)
	cmd := exec.Command("iptables", args...)
	err := cmd.Run()
	return err == nil, nil
}
