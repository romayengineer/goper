package capture

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewEntryIDFormat(t *testing.T) {
	id := NewEntryID()
	s := string(id)

	assert.Regexp(t, `^\d{14}-\d+$`, s, "id %q should match timestamp-counter format", s)
}

func TestNewEntryIDUniqueness(t *testing.T) {
	seen := make(map[EntryID]bool)
	for i := 0; i < 1000; i++ {
		id := NewEntryID()
		assert.False(t, seen[id], "duplicate id %q", id)
		seen[id] = true
	}
}

func TestNewEntryIDCounterRestarts(t *testing.T) {
	old := idCounter
	idCounter = 0
	defer func() { idCounter = old }()

	id := NewEntryID()
	assert.True(t, strings.HasSuffix(string(id), "-1"), "expected counter restart at 1, got %q", id)
}
