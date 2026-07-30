package tui

import (
	"fmt"
	"regexp"
	"strconv"
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

// renderDiff renders a "+ /- /  "-prefixed whole-file diff as git-style rows:
// added lines get a full-width green band, removed lines a red band, and each
// row carries a line-number gutter (old line for removals, new line for context
// and additions). Unchanged runs beyond 3 lines of context collapse to a "⋮"
// marker; output caps at maxLines. width is the full band width (already net of
// the caller's indent). Returns fully styled rows.
func renderDiff(diff string, width, maxLines int) []string {
	raw := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	if len(raw) == 1 && raw[0] == "" {
		return nil
	}
	prefix := func(l string) (mark byte, code string, ok bool) {
		if len(l) >= 2 && (l[:2] == "  " || l[:2] == "+ " || l[:2] == "- ") {
			return l[0], l[2:], true
		}
		return 0, l, false
	}
	isChange := func(l string) bool { return strings.HasPrefix(l, "+ ") || strings.HasPrefix(l, "- ") }

	// keep[] marks changed lines plus 3 lines of surrounding context.
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

	numW := len(strconv.Itoa(len(raw) + 1))
	if numW < 3 {
		numW = 3
	}
	addBand := lipgloss.NewStyle().Foreground(lipgloss.Color("#CDEAD5")).Background(lipgloss.Color("#123322")).Width(width)
	delBand := lipgloss.NewStyle().Foreground(lipgloss.Color("#F1CCD1")).Background(lipgloss.Color("#3A1A1E")).Width(width)
	ctxStyle := lipgloss.NewStyle().Foreground(dim)
	gapStyle := lipgloss.NewStyle().Foreground(muted)

	row := func(band lipgloss.Style, num int, mark byte, code string) string {
		numStr := ""
		if num > 0 {
			numStr = strconv.Itoa(num)
		}
		avail := width - numW - 3
		if avail < 1 {
			avail = 1
		}
		text := fmt.Sprintf("%*s %c %s", numW, numStr, mark, truncate(code, avail))
		return band.Render(text)
	}

	var out []string
	oldLn, newLn := 0, 0 // 1-based counters; incremented as we pass each raw line
	gap := false
	for i, l := range raw {
		mark, code, ok := prefix(l)
		switch {
		case !ok: // truncation note or stray line; no counter advance
		case mark == ' ':
			oldLn++
			newLn++
		case mark == '-':
			oldLn++
		case mark == '+':
			newLn++
		}
		if !keep[i] {
			gap = true
			continue
		}
		if gap {
			out = append(out, gapStyle.Render(diffGap))
			gap = false
		}
		switch {
		case !ok:
			out = append(out, gapStyle.Render("  "+l))
		case mark == '+':
			out = append(out, row(addBand, newLn, '+', code))
		case mark == '-':
			out = append(out, row(delBand, oldLn, '-', code))
		default:
			// context row: dim gutter + dim code, no band.
			numStr := strconv.Itoa(newLn)
			avail := width - numW - 3
			if avail < 1 {
				avail = 1
			}
			out = append(out, ctxStyle.Render(fmt.Sprintf("%*s   %s", numW, numStr, truncate(code, avail))))
		}
		if len(out) >= maxLines {
			out = append(out, gapStyle.Render(diffGap+" (diff truncated — ctrl+o for full)"))
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
