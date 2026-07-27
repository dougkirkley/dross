package agent

import (
	"strings"
	"testing"
)

func TestDiffStatNewFile(t *testing.T) {
	added, removed, diff := diffStat("", "a\nb\nc")
	if added != 3 || removed != 0 {
		t.Fatalf("new file: added=%d removed=%d", added, removed)
	}
	if strings.Count(diff, "+ ") != 3 {
		t.Fatalf("expected 3 added lines in diff:\n%s", diff)
	}
}

func TestDiffStatEdit(t *testing.T) {
	old := "one\ntwo\nthree"
	neu := "one\nTWO\nthree\nfour"
	added, removed, diff := diffStat(old, neu)
	// "two" -> "TWO" is one remove + one add; "four" is one add.
	if added != 2 || removed != 1 {
		t.Fatalf("edit: added=%d removed=%d\n%s", added, removed, diff)
	}
	// Unchanged lines are kept as context.
	if !strings.Contains(diff, "  one") || !strings.Contains(diff, "  three") {
		t.Fatalf("context lines missing:\n%s", diff)
	}
}

func TestDiffStatNoChange(t *testing.T) {
	added, removed, _ := diffStat("same\ntext", "same\ntext")
	if added != 0 || removed != 0 {
		t.Fatalf("no change should be 0/0, got %d/%d", added, removed)
	}
}
