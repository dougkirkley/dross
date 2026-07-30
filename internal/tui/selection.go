package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// nextRE matches a "[next] <prompt>" line the model may append to propose a
// follow-up. Only present when there's a real next step.
var nextRE = regexp.MustCompile(`(?im)^[ \t]*\[next\][ \t]*:?[ \t]*(.+?)[ \t]*$`)

// extractNext pulls a trailing [next] suggestion out of assistant text, returning
// the text with that line removed and the suggested prompt ("" if none).
func extractNext(s string) (clean, next string) {
	locs := nextRE.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return s, ""
	}
	last := locs[len(locs)-1] // use the final [next] line
	next = strings.TrimSpace(s[last[2]:last[3]])
	clean = strings.TrimRight(s[:last[0]]+s[last[1]:], " \t\n")
	return clean, next
}

// diffGap marks collapsed unchanged regions in a rendered diff.
const diffGap = "  ⋮"

// diffLines formats a "+ /- /  "-prefixed diff for display: it keeps changed
// lines and up to 3 context lines around each, collapsing longer unchanged runs
// to a "⋮" marker. Caps at maxLines total (with a trailing note) so a huge diff
// doesn't flood the feed — the full version is available via ctrl+o / the tool
// view.
func diffLines(diff string, maxLines int) []string {
	raw := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	if len(raw) == 1 && raw[0] == "" {
		return nil
	}
	isChange := func(l string) bool {
		return strings.HasPrefix(l, "+ ") || strings.HasPrefix(l, "- ")
	}
	const ctx = 3
	keep := make([]bool, len(raw))
	for i, l := range raw {
		if !isChange(l) {
			continue
		}
		lo, hi := i-ctx, i+ctx
		if lo < 0 {
			lo = 0
		}
		if hi >= len(raw) {
			hi = len(raw) - 1
		}
		for j := lo; j <= hi; j++ {
			keep[j] = true
		}
	}
	var out []string
	gap := false
	for i, l := range raw {
		if !keep[i] {
			gap = true
			continue
		}
		if gap {
			out = append(out, diffGap)
			gap = false
		}
		out = append(out, l)
		if len(out) >= maxLines {
			out = append(out, diffGap+" (diff truncated — ctrl+o for full)")
			break
		}
	}
	return out
}

// selPt is a point in the transcript: a line index into m.lines and a rune
// column into that line's plain (ANSI-stripped) text.
type selPt struct{ line, col int }

func (p selPt) before(q selPt) bool {
	if p.line != q.line {
		return p.line < q.line
	}
	return p.col < q.col
}

// selBounds returns the selection ordered start→end.
func (m Model) selBounds() (selPt, selPt) {
	if m.selB.before(m.selA) {
		return m.selB, m.selA
	}
	return m.selA, m.selB
}

// mouseToContent maps a mouse (x,y) in the body region to a transcript line +
// rune column. The body sits below the 1-line header and shows m.vp from
// YOffset. ok is false when the click isn't over transcript text.
func (m Model) mouseToContent(x, y int) (selPt, bool) {
	row := y - 1 // header is one line
	if row < 0 || row >= m.vp.Height {
		return selPt{}, false
	}
	line := m.vp.YOffset + row
	if line < 0 || line >= len(m.lines) {
		return selPt{}, false
	}
	col := x
	if n := len([]rune(stripANSI(m.lines[line]))); col > n {
		col = n
	}
	if col < 0 {
		col = 0
	}
	return selPt{line: line, col: col}, true
}

// selectionText returns the plain text covered by the current selection.
func (m Model) selectionText() string {
	if !m.hasSel {
		return ""
	}
	a, b := m.selBounds()
	if a.line == b.line {
		r := []rune(stripANSI(m.lines[a.line]))
		return string(r[clamp(a.col, 0, len(r)):clamp(b.col, 0, len(r))])
	}
	var sb strings.Builder
	first := []rune(stripANSI(m.lines[a.line]))
	sb.WriteString(string(first[clamp(a.col, 0, len(first)):]))
	sb.WriteByte('\n')
	for l := a.line + 1; l < b.line; l++ {
		sb.WriteString(stripANSI(m.lines[l]))
		sb.WriteByte('\n')
	}
	last := []rune(stripANSI(m.lines[b.line]))
	sb.WriteString(string(last[:clamp(b.col, 0, len(last))]))
	return sb.String()
}

// transcriptContent is the viewport body: the joined transcript, with the
// active selection highlighted. Selected lines drop their per-token color (they
// render in the selection style over plain text), which is fine for a highlight.
func (m Model) transcriptContent() string {
	if !m.hasSel {
		return strings.Join(m.lines, "\n")
	}
	a, b := m.selBounds()
	sel := lipgloss.NewStyle().Reverse(true)
	out := make([]string, len(m.lines))
	for i, line := range m.lines {
		if i < a.line || i > b.line {
			out[i] = line
			continue
		}
		r := []rune(stripANSI(line))
		start, end := 0, len(r)
		if i == a.line {
			start = clamp(a.col, 0, len(r))
		}
		if i == b.line {
			end = clamp(b.col, 0, len(r))
		}
		if start > end {
			start = end
		}
		out[i] = string(r[:start]) + sel.Render(string(r[start:end])) + string(r[end:])
	}
	return strings.Join(out, "\n")
}

func (m *Model) clearSelection() {
	m.selecting, m.selMoved, m.hasSel = false, false, false
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
