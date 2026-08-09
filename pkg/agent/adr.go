package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Architecture decision records: the reasoning a repository keeps about itself.
//
// An ADR is the one class of document written specifically to be read later by
// somebody who was not in the room. That makes it exactly what an agent should
// consult before proposing a change, and dross could not see them at all: the
// prompt ingested AGENTS.md, CLAUDE.md and README.md, so a repository could
// hold a numbered, dated record of why a thing is the way it is and a session
// would quote the README instead.
//
// Three things happen here, and the order matters. They are listed, so the
// model knows a decision exists. Accepted ones are framed as constraints rather
// than context, because a decision somebody wrote down and accepted is not a
// suggestion. And there is a tool to add one, because the alternative — a
// decision made in a session and recorded nowhere — is how a repository ends up
// with reasoning that lives only in a chat log.

// adrDirs are the conventional locations, in the order they are looked for.
// The first that exists wins; a repository with two of these has bigger
// problems than this code can solve.
var adrDirs = []string{
	"docs/adr", "docs/adrs", "docs/decisions", "docs/architecture/decisions",
	"doc/adr", "adr",
}

// adr is one record, as much of it as the prompt needs.
type adr struct {
	num    int
	title  string
	status string
	path   string // workspace-relative
}

// adrTitle matches the conventional heading: "# 1. Some decision", with the
// number optional so a file that skipped it is still listed rather than lost.
var adrTitle = regexp.MustCompile(`^#\s*(?:(\d+)[.)]?\s*)?(.+?)\s*$`)

// adrLocation returns the workspace's ADR directory, or "" if it has none.
func adrLocation(workdir string) string {
	if workdir == "" {
		return ""
	}
	for _, d := range adrDirs {
		full := filepath.Join(workdir, d)
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			return d
		}
	}
	return ""
}

// findADRs reads the records in a workspace, cheapest-first: the heading and
// the status line, never the whole document. Ten ADRs read in full would crowd
// out the prompt they are meant to inform.
func findADRs(workdir string) (dir string, out []adr) {
	dir = adrLocation(workdir)
	if dir == "" {
		return "", nil
	}
	entries, err := os.ReadDir(filepath.Join(workdir, dir))
	if err != nil {
		return dir, nil
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		if strings.EqualFold(name, "README.md") || strings.EqualFold(name, "index.md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(workdir, dir, name))
		if err != nil {
			continue
		}
		rec := adr{path: filepath.Join(dir, name), title: strings.TrimSuffix(name, filepath.Ext(name))}
		// Number from the filename first — 0001-foo.md is the convention and is
		// more reliable than a heading somebody may have reworded.
		if m := regexp.MustCompile(`^(\d+)`).FindStringSubmatch(name); m != nil {
			rec.num, _ = strconv.Atoi(m[1])
		}
		lines := strings.Split(string(body), "\n")
		inStatus, inFence, gotTitle := false, false, false
		for _, ln := range lines {
			t := strings.TrimSpace(ln)
			// A fenced block is content, not structure. This ADR quotes a
			// kernel config, and lines like "# CONFIG_USBIP_VUDC is not set"
			// look exactly like headings — the title came out as that one,
			// because the loop took the last "# " in the file rather than the
			// first and had no idea it was inside a code block.
			if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			switch {
			case !gotTitle && strings.HasPrefix(t, "# "):
				if m := adrTitle.FindStringSubmatch(t); m != nil {
					if m[1] != "" && rec.num == 0 {
						rec.num, _ = strconv.Atoi(m[1])
					}
					rec.title = m[2]
					gotTitle = true // the first H1 is the decision; later ones are prose
				}
			case strings.EqualFold(t, "## Status"):
				inStatus = true
			case strings.HasPrefix(t, "## "):
				inStatus = false
			case inStatus && t != "" && rec.status == "":
				rec.status = strings.TrimSuffix(t, ".")
			}
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].num != out[j].num {
			return out[i].num < out[j].num
		}
		return out[i].path < out[j].path
	})
	return dir, out
}

// accepted reports whether a status makes the record binding. Anything
// superseded, rejected or still proposed is history or a draft, and treating
// those as constraints would freeze a repository to decisions it has already
// moved past.
func (a adr) accepted() bool {
	s := strings.ToLower(a.status)
	return strings.HasPrefix(s, "accept")
}

// adrGuidance lists the records and says what they are worth.
//
// Titles and statuses only. A model that knows a decision exists can open it,
// and one that is handed ten decisions in full has no room left for the task.
func adrGuidance(dir string, records []adr) string {
	if len(records) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Decisions on record\n\n" +
		"This project keeps architecture decision records in " + dir + ". " +
		"Only their titles are here; read one before changing anything it covers.\n")
	for _, r := range records {
		status := r.status
		if status == "" {
			status = "no status"
		}
		if r.num > 0 {
			fmt.Fprintf(&b, "\n- %d. %s — %s (%s)", r.num, r.title, status, r.path)
		} else {
			fmt.Fprintf(&b, "\n- %s — %s (%s)", r.title, status, r.path)
		}
	}
	b.WriteString("\n\nAn accepted record is a constraint, not background reading. " +
		"Work within it. If the task cannot be done without contradicting one, say which " +
		"record and why before doing it, rather than quietly doing the opposite of what " +
		"somebody wrote down and agreed. A record that is superseded or rejected is history: " +
		"useful for why things are as they are, not binding on what you do next. " +
		"When the user settles an architectural question in this session, record it with the " +
		"decision tool — a decision that exists only in a chat log is one the next person cannot find.")
	return b.String()
}

// nextADRNumber is one past the highest on record, so numbering survives a gap
// and never silently reuses one.
func nextADRNumber(records []adr) int {
	n := 0
	for _, r := range records {
		if r.num > n {
			n = r.num
		}
	}
	return n + 1
}

// adrSlug turns a title into the filename half of the convention.
func adrSlug(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		s = "decision"
	}
	return s
}

// renderADR writes the record in the shape the rest of the directory uses.
func renderADR(num int, title, date, context, decision, consequences string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %d. %s\n\nDate: %s\n\n## Status\n\nAccepted.\n", num, title, date)
	section := func(name, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", name, body)
	}
	section("Context", context)
	section("Decision", decision)
	section("Consequences", consequences)
	return b.String()
}

// recordDecision backs the `decision` tool: list what is on record, or add one.
//
// It writes into the directory the project already uses and follows the
// numbering and shape of what is there. It will not create the directory: a
// repository that keeps no decision records has not asked for one, and an agent
// inventing a docs/adr tree because it felt architectural is exactly the kind
// of unasked-for change that makes a tool untrustworthy.
func (a *Agent) recordDecision(op, title, context, decision, consequences string) (string, bool) {
	dir, records := findADRs(a.cfg.Workdir)
	if dir == "" {
		return "this project keeps no decision records; create one of " +
			strings.Join(adrDirs[:3], ", ") + " first if it should", true
	}
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "list", "":
		if len(records) == 0 {
			return "no decisions recorded in " + dir + " yet", false
		}
		var b strings.Builder
		for _, r := range records {
			status := r.status
			if status == "" {
				status = "no status"
			}
			fmt.Fprintf(&b, "%d. %s — %s (%s)\n", r.num, r.title, status, r.path)
		}
		return b.String(), false
	case "record":
		if strings.TrimSpace(title) == "" {
			return "a decision needs a title: the decision itself, in one line", true
		}
		if strings.TrimSpace(decision) == "" {
			return "a decision needs a decision: what was chosen, stated plainly", true
		}
		if strings.TrimSpace(context) == "" {
			return "a decision needs its context: the problem that forced the choice. " +
				"A record of what without why is the half nobody can use later", true
		}
		num := nextADRNumber(records)
		name := fmt.Sprintf("%04d-%s.md", num, adrSlug(title))
		rel := filepath.Join(dir, name)
		body := renderADR(num, title, time.Now().Format("2006-01-02"), context, decision, consequences)
		if _, err := a.ws.writeFile(rel, body); err != nil {
			return err.Error(), true
		}
		a.emit("text", "recorded decision "+rel)
		return fmt.Sprintf("recorded %s. It is a file in the project — it belongs in the same "+
			"commit as the change it justifies.", rel), false
	}
	return fmt.Sprintf("unknown op %q; use record or list", op), true
}
