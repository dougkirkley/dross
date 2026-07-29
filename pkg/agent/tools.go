package agent

import (
	"bufio"
	"context"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style|head|nav|footer)[^>]*>.*?</(script|style|head|nav|footer)>`)
	reTag         = regexp.MustCompile(`(?s)<[^>]+>`)
	reBlankLines  = regexp.MustCompile(`\n[ \t]*\n(\s*\n)+`)
)

// webFetch GETs a URL and returns its text (HTML reduced to readable text),
// size-capped. Only http(s) is allowed.
func webFetch(ctx context.Context, url string) (string, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("url must start with http:// or https://")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ergon/1.0 (+coding-agent)")
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	text := string(body)
	if strings.Contains(resp.Header.Get("Content-Type"), "html") || strings.Contains(text, "<html") {
		text = stripHTML(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Sprintf("[%d %s, no readable text]", resp.StatusCode, resp.Status), nil
	}
	if len(text) > 100_000 {
		text = text[:100_000] + "\n…[truncated]"
	}
	return text, nil
}

// stripHTML reduces HTML to readable text: drops script/style/nav/etc, removes
// tags, unescapes entities, and collapses excess blank lines.
func stripHTML(s string) string {
	s = reScriptStyle.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return s
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// workspace confines every file and shell operation to a single root directory.
// Tool inputs are model-generated and untrusted; every path is resolved and
// checked to stay within root before any I/O. Full permission gating (the
// manual/auto/accept-edits/plan modes) is phase 4 — for now the workspace root
// is the boundary and bash runs with a timeout.
type workspace struct {
	root string
}

func newWorkspace(root string) (*workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace %s is not a directory", abs)
	}
	return &workspace{root: abs}, nil
}

// grep searches file contents under path (or the whole workspace) for a regexp,
// returning matches as "rel:line: text". Skips VCS/dep dirs and binary/large
// files, and caps the result.
func (w *workspace) grep(pattern, path string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("bad pattern: %w", err)
	}
	base := w.root
	if path != "" {
		abs, err := w.resolve(path)
		if err != nil {
			return "", err
		}
		base = abs
	}
	const maxMatches = 300
	var out strings.Builder
	matches := 0
	walkErr := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && p != base {
				return filepath.SkipDir
			}
			return nil
		}
		if matches >= maxMatches {
			return io.EOF
		}
		if info, err := d.Info(); err == nil && info.Size() > 1<<20 {
			return nil // skip large files
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(w.root, p)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		ln := 0
		for sc.Scan() {
			ln++
			line := sc.Text()
			if !utf8.ValidString(line) {
				return nil // binary file
			}
			if re.MatchString(line) {
				fmt.Fprintf(&out, "%s:%d: %s\n", rel, ln, truncate(strings.TrimSpace(line), 300))
				matches++
				if matches >= maxMatches {
					break
				}
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != io.EOF {
		return "", walkErr
	}
	if matches == 0 {
		return "[no matches]", nil
	}
	res := out.String()
	if matches >= maxMatches {
		res += fmt.Sprintf("[truncated at %d matches]\n", maxMatches)
	}
	return res, nil
}

// glob lists workspace files matching a glob pattern (supports ** ).
func (w *workspace) glob(pattern string) (string, error) {
	re, err := globToRegex(pattern)
	if err != nil {
		return "", err
	}
	var out []string
	filepath.WalkDir(w.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && p != w.root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(w.root, p)
		if re.MatchString(rel) {
			out = append(out, rel)
		}
		return nil
	})
	if len(out) == 0 {
		return "[no files]", nil
	}
	sort.Strings(out)
	if len(out) > 500 {
		out = append(out[:500], "[truncated]")
	}
	return strings.Join(out, "\n"), nil
}

func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "dist", "build":
		return true
	}
	return false
}

// globToRegex converts a glob (with ** for any depth, * within a segment) into an
// anchored regexp over the forward-slash path.
func globToRegex(g string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				b.WriteString(".*")
				i++
				if i+1 < len(g) && g[i+1] == '/' {
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '(', ')', '+', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// resolve turns a tool-supplied path (relative to the workspace, or absolute)
// into an absolute path proven to sit inside root. Rejects traversal.
func (w *workspace) resolve(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(w.root, p)
	}
	// Clean already collapsed .. segments; verify the result is under root.
	rel, err := filepath.Rel(w.root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", p)
	}
	return abs, nil
}

const maxReadBytes = 256 * 1024

// readFile returns line-numbered content so the model can reason about ranges.
func (w *workspace) readFile(path string) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.Size() > maxReadBytes {
		return "", fmt.Errorf("file is %d bytes; too large to read (limit %d) — use bash (sed/grep) to view a slice", info.Size(), maxReadBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}
	return b.String(), nil
}

// writeFile creates or overwrites a file, creating parent dirs.
func (w *workspace) writeFile(path, content string) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}

// editFile does an exact-string replacement that must match exactly once.
// Ambiguous (>1) or absent (0) matches are errors with a legible message — the
// quality of this one tool disproportionately shapes how good the agent feels.
func (w *workspace) editFile(path, oldStr, newStr string) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	text := string(data)
	n := strings.Count(text, oldStr)
	switch {
	case n == 0:
		return "", fmt.Errorf("old_str not found in %s; it must match the file exactly including whitespace", path)
	case n > 1:
		return "", fmt.Errorf("old_str matched %d times in %s; add surrounding context so it is unique", n, path)
	}
	updated := strings.Replace(text, oldStr, newStr, 1)
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (1 replacement)", path), nil
}

// diffStat produces a line-level diff of oldText vs newText: the number of
// added and removed lines plus a unified-ish diff (each line prefixed "+ ", "- "
// or "  "). Uses a standard LCS so unchanged lines are preserved as context —
// this backs the mobile "Edited file +A -B" activity + expandable diff view.
func diffStat(oldText, newText string) (added, removed int, unified string) {
	var o, n []string
	if oldText != "" {
		o = strings.Split(strings.TrimRight(oldText, "\n"), "\n")
	}
	if newText != "" {
		n = strings.Split(strings.TrimRight(newText, "\n"), "\n")
	}
	m, k := len(o), len(n)
	// lcs[i][j] = length of the longest common subsequence of o[i:] and n[j:].
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, k+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := k - 1; j >= 0; j-- {
			if o[i] == n[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var b strings.Builder
	i, j := 0, 0
	for i < m && j < k {
		switch {
		case o[i] == n[j]:
			b.WriteString("  " + o[i] + "\n")
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			b.WriteString("- " + o[i] + "\n")
			removed++
			i++
		default:
			b.WriteString("+ " + n[j] + "\n")
			added++
			j++
		}
	}
	for ; i < m; i++ {
		b.WriteString("- " + o[i] + "\n")
		removed++
	}
	for ; j < k; j++ {
		b.WriteString("+ " + n[j] + "\n")
		added++
	}
	diff := b.String()
	const cap = 12000 // keep events light; the counts stay exact
	if len(diff) > cap {
		diff = diff[:cap] + "\n… (diff truncated)\n"
	}
	return added, removed, diff
}

// readForPublish reads a workspace file and reports its bytes, MIME type
// (extension first, then content sniffing), and base name — for the publish
// tool.
func (w *workspace) readForPublish(path string) (data []byte, mimeType, name string, err error) {
	abs, err := w.resolve(path)
	if err != nil {
		return nil, "", "", err
	}
	data, err = os.ReadFile(abs)
	if err != nil {
		return nil, "", "", err
	}
	name = filepath.Base(path)
	mimeType = mime.TypeByExtension(filepath.Ext(abs))
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, name, nil
}

const bashTimeout = 120 * time.Second

// flexSeconds accepts a duration-in-seconds as either a JSON number (10) or a
// string ("10"), since the claude-code driven mode sends all params as strings.
type flexSeconds float64

func (f *flexSeconds) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // ignore garbage; caller's default applies
	}
	*f = flexSeconds(v)
	return nil
}

func (f flexSeconds) durationOr(def time.Duration) time.Duration {
	if f <= 0 {
		return def
	}
	return time.Duration(float64(f) * float64(time.Second))
}

// monitor polls command until it exits 0 (condition met) or the timeout elapses.
// It's how the agent waits on async state — a server coming up, CI finishing, a
// file appearing — without spending turns re-checking.
func (w *workspace) monitor(command string, interval, timeout time.Duration) (string, error) {
	ctx, cancel := contextWithTimeout(timeout)
	defer cancel()
	start := time.Now()
	checks := 0
	for {
		checks++
		cmd := exec.CommandContext(ctx, "bash", "-c", command)
		cmd.Dir = w.root
		out, err := cmd.CombinedOutput()
		if err == nil {
			body := strings.TrimSpace(string(out))
			if body == "" {
				body = "[no output]"
			}
			return fmt.Sprintf("condition met after %s (%d check(s))\n%s",
				time.Since(start).Round(time.Second), checks, body), nil
		}
		if ctx.Err() != nil {
			return fmt.Sprintf("timed out after %s (%d check(s)); condition never met. last output:\n%s",
				timeout, checks, strings.TrimSpace(string(out))), nil
		}
		select {
		case <-ctx.Done():
			return fmt.Sprintf("timed out after %s (%d check(s)); condition never met.", timeout, checks), nil
		case <-time.After(interval):
		}
	}
}

// runBash executes a command with the workspace as cwd, capturing combined
// output and exit code. Bounded by a timeout. Phase 4 adds permission gating.
func (w *workspace) runBash(command string) (string, error) {
	ctx, cancel := contextWithTimeout(bashTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = w.root
	out, err := cmd.CombinedOutput()

	result := string(out)
	if ctx.Err() != nil {
		return result, fmt.Errorf("command timed out after %s", bashTimeout)
	}
	if err != nil {
		// Non-zero exit is information for the model, not a tool failure —
		// return the output and the exit status as normal content.
		return fmt.Sprintf("%s\n[exit status: %v]", result, err), nil
	}
	if strings.TrimSpace(result) == "" {
		return "[no output]", nil
	}
	return result, nil
}
