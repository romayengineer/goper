package capture

import (
	"strings"
	"testing"
)

func TestNewEntryIDFormat(t *testing.T) {
	id := NewEntryID()
	s := string(id)

	if len(s) < len("20060102150405-1") {
		t.Fatalf("id %q too short", s)
	}

	for _, r := range s[:14] {
		if r < '0' || r > '9' {
			t.Fatalf("id %q: first 14 chars must be digits, got %q", s, r)
		}
	}
	if s[14] != '-' {
		t.Fatalf("id %q: char 14 must be '-', got %q", s, s[14])
	}
}

func TestNewEntryIDUniqueness(t *testing.T) {
	seen := make(map[EntryID]bool)
	for i := 0; i < 1000; i++ {
		id := NewEntryID()
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestNewEntryIDCounterRestarts(t *testing.T) {
	old := idCounter
	idCounter = 0
	defer func() { idCounter = old }()

	id := NewEntryID()
	if !strings.HasSuffix(string(id), "-1") {
		t.Fatalf("expected counter restart at 1, got %q", id)
	}
}
