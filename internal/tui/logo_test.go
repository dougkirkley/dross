package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestBigLogoRows proves every glyph is 5 rows of equal width, so the assembled
// logo can never drift out of alignment.
func TestBigLogoRows(t *testing.T) {
	rows := bigLogoRows("ERGON")
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	// Compare display width (cells), not bytes — █ is multi-byte.
	w := lipgloss.Width(rows[0])
	for i, r := range rows {
		if lipgloss.Width(r) != w {
			t.Fatalf("row %d width = %d, want %d (misaligned)", i, lipgloss.Width(r), w)
		}
	}
	// All glyphs present (no letters silently dropped).
	for _, ch := range "ERGON" {
		if _, ok := glyphs[ch]; !ok {
			t.Fatalf("missing glyph %q", ch)
		}
	}
}

func TestRenderLogoVariants(t *testing.T) {
	for v := 0; v < 4; v++ {
		out := renderLogo(v)
		if strings.TrimSpace(out) == "" {
			t.Fatalf("variant %d rendered empty", v)
		}
	}
}

func TestWrap(t *testing.T) {
	got := wrap("the quick brown fox", 9)
	want := []string{"the quick", "brown fox"}
	if len(got) != len(want) {
		t.Fatalf("wrap = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wrap[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// A word longer than the width is not split (stays whole on its line).
	if g := wrap("supercalifragilistic word", 5); g[0] != "supercalifragilistic" {
		t.Fatalf("long word split: %v", g)
	}
	// width < 1 returns the input unchanged.
	if g := wrap("abc", 0); len(g) != 1 || g[0] != "abc" {
		t.Fatalf("wrap width<1: %v", g)
	}
}

// TestDemoViews render without panicking and include the logo/prompt.
func TestDemoViews(t *testing.T) {
	if v := DemoWelcomeView(90, 26, 1); !strings.Contains(v, "pluggable") {
		t.Fatal("welcome view missing tagline")
	}
	if v := DemoView(96, 28); strings.TrimSpace(v) == "" {
		t.Fatal("demo view empty")
	}
}
