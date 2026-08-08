package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
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
	// DuckDuckGo HTML result rows: a result link (title+href) and its snippet.
	reResultLink    = regexp.MustCompile(`(?is)<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reResultSnippet = regexp.MustCompile(`(?is)class="result__snippet"[^>]*>(.*?)</a>`)
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
	req.Header.Set("User-Agent", "dross/1.0 (+coding-agent)")
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	text := string(body)
	// A page the turn actually read, which is a stronger citation than one it
	// merely saw in a result list. Recorded on the response so a fetch that
	// failed does not become a source.
	if src, ok := NewSource(pageTitle(text), url, ""); ok {
		recordSource(ctx, src)
	}
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

// searchLimit is how many results web_search returns.
const searchLimit = 8

// webSearch returns a ranked list of results (title / url / snippet) so the
// model can then web_fetch the ones it wants. Provider-agnostic. Uses a real
// search API when a key is set — Exa (EXA_API_KEY) or Brave (BRAVE_API_KEY /
// DROSS_SEARCH_KEY), both free-tier, no card — and falls back to a keyless
// DuckDuckGo scrape otherwise (flaky; DDG rate-limits automated requests).
func webSearch(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is empty")
	}
	if key := os.Getenv("EXA_API_KEY"); key != "" {
		if out, err := exaSearch(ctx, query, key); err == nil {
			return out, nil
		}
	}
	if key := braveKey(); key != "" {
		if out, err := braveSearch(ctx, query, key); err == nil {
			return out, nil
		}
	}
	return ddgSearch(ctx, query)
}

func braveKey() string {
	if k := os.Getenv("BRAVE_API_KEY"); k != "" {
		return k
	}
	return os.Getenv("DROSS_SEARCH_KEY")
}

// exaSearch queries the Exa Search API (built for LLMs; free key at
// dashboard.exa.ai, no card) and formats results with highlight snippets.
func exaSearch(ctx context.Context, query, key string) (string, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"query":      query,
		"numResults": searchLimit,
		"contents":   map[string]any{"highlights": true},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.exa.ai/search", strings.NewReader(string(reqBody)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	hc := &http.Client{Timeout: 25 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exa search: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Results []struct {
			Title      string   `json:"title"`
			URL        string   `json:"url"`
			Highlights []string `json:"highlights"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return "", err
	}
	if len(payload.Results) == 0 {
		return fmt.Sprintf("[no results for %q]", query), nil
	}
	var b strings.Builder
	for i, r := range payload.Results {
		if i >= searchLimit {
			break
		}
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, strings.TrimSpace(r.Title), r.URL)
		snippet := strings.TrimSpace(strings.Join(r.Highlights, " … "))
		if snippet != "" {
			fmt.Fprintf(&b, "   %s\n", snippet)
		}
		// Recorded here as well as in the other backends: a citation has to
		// survive whichever one answered, and the one that answers changes with
		// whether a key is set.
		if src, ok := NewSource(r.Title, r.URL, snippet); ok {
			recordSource(ctx, src)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// braveSearch queries the Brave Search API and formats the web results.
func braveSearch(ctx context.Context, query, key string) (string, error) {
	endpoint := "https://api.search.brave.com/res/v1/web/search?count=" +
		strconv.Itoa(searchLimit) + "&q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("brave search: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return "", err
	}
	if len(payload.Web.Results) == 0 {
		return fmt.Sprintf("[no results for %q]", query), nil
	}
	var b strings.Builder
	for i, r := range payload.Web.Results {
		if i >= searchLimit {
			break
		}
		title := strings.TrimSpace(stripHTML(r.Title))
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, title, r.URL)
		if src, ok := NewSource(title, r.URL, strings.TrimSpace(stripHTML(r.Description))); ok {
			recordSource(ctx, src)
		}
		if s := strings.TrimSpace(stripHTML(r.Description)); s != "" {
			fmt.Fprintf(&b, "   %s\n", s)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ddgSearch is the keyless fallback: DuckDuckGo's HTML endpoint, scraped.
func ddgSearch(ctx context.Context, query string) (string, error) {
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	// A browser-like UA; DuckDuckGo returns an empty page to unknown agents.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0 Safari/537.36")
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MB cap
	page := string(body)

	links := reResultLink.FindAllStringSubmatch(page, -1)
	snips := reResultSnippet.FindAllStringSubmatch(page, -1)
	if len(links) == 0 {
		return fmt.Sprintf("[no results for %q]", query), nil
	}
	const maxResults = 8
	var b strings.Builder
	for i, m := range links {
		if i >= maxResults {
			break
		}
		title := strings.TrimSpace(stripHTML(m[2]))
		link := ddgResolve(m[1])
		snippet := ""
		if i < len(snips) {
			snippet = strings.TrimSpace(stripHTML(snips[i][1]))
		}
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, title, link)
		if snippet != "" {
			fmt.Fprintf(&b, "   %s\n", snippet)
		}
		// Recorded as the result is formatted, so what a turn cites is what it
		// was actually handed rather than what the model remembers of it.
		if src, ok := NewSource(title, link, snippet); ok {
			recordSource(ctx, src)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ddgResolve turns a DuckDuckGo result href into the real destination URL. DDG
// wraps links as //duckduckgo.com/l/?uddg=<encoded-target>; unwrap when present.
func ddgResolve(href string) string {
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if t := u.Query().Get("uddg"); t != "" {
		if dec, err := url.QueryUnescape(t); err == nil {
			return dec
		}
	}
	return href
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
	root  string
	tools string   // sandbox: where SDKs/toolchains install ($DROSS_TOOLS); "" if not sandboxed
	env   []string // sandbox: scoped process env for bash/monitor; nil = inherit parent env
	// confined refuses any path outside root. On for a sandboxed session —
	// anything driven remotely, where nobody is watching what it opens — and
	// off for a local one, where being unable to read the repository next door
	// is an obstacle rather than a protection.
	confined bool
}

func newWorkspace(root string, sandbox bool) (*workspace, error) {
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
	w := &workspace{root: abs, confined: sandbox}
	if sandbox {
		if err := w.initSandbox(); err != nil {
			return nil, err
		}
	}
	return w, nil
}

// sandboxDir is the hidden root under the workspace holding the session's HOME
// and installed toolchains. Kept out of file listings/search (see skipDir).
const sandboxDir = ".dross"

// initSandbox inherits the host's full environment and binaries, then redirects
// every default write/install location into a hidden dir under the workspace.
// The model keeps access to all global tools and env vars, but anything it
// installs (go, node, npm, pip, cargo, a downloaded SDK) lands inside this
// session — never in the host's real HOME or system dirs — and is immediately on
// PATH. Note: this scopes where tools write by convention; hard filesystem
// isolation is the container's job (e.g. a read-only rootfs with the workspace
// as the only writable mount).
func (w *workspace) initSandbox() error {
	home := filepath.Join(w.root, sandboxDir, "home")
	tools := filepath.Join(w.root, sandboxDir, "tools")
	dirs := []string{
		home, tools,
		filepath.Join(tools, "bin"),
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".local", "bin"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("sandbox %s: %w", d, err)
		}
	}
	w.tools = tools

	// PATH: sandbox bins first (so installed tools win and a freshly-installed
	// toolchain is usable in the next command), then the inherited PATH so all
	// global binaries still resolve. $DROSS_TOOLS/<name>/bin is the convention
	// for an extracted SDK (e.g. Go at $DROSS_TOOLS/go).
	binDirs := []string{
		filepath.Join(tools, "bin"),
		filepath.Join(tools, "go", "bin"),
		filepath.Join(tools, "node", "bin"),
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".local", "bin"),
	}
	path := strings.Join(binDirs, string(os.PathListSeparator))
	if base := os.Getenv("PATH"); base != "" {
		path += string(os.PathListSeparator) + base
	}

	// Inherit the whole parent env, then append overrides. exec uses the last
	// value for a duplicate key, so these win while everything else carries over.
	// Inherit the host environment (so global tooling keeps working) minus
	// anything that looks like a credential: the model can run `env`, and the
	// provider keys this process holds are not the shell's business.
	env := append(withoutSecrets(os.Environ()),
		"HOME="+home,
		"PATH="+path,
		"DROSS_WORKSPACE="+w.root,
		"DROSS_TOOLS="+tools,
		"GOPATH="+filepath.Join(home, "go"),
		"GOBIN="+filepath.Join(home, "go", "bin"),
		"GOCACHE="+filepath.Join(home, ".cache", "go-build"),
		"npm_config_prefix="+filepath.Join(home, ".npm-global"),
		"npm_config_cache="+filepath.Join(home, ".npm-cache"),
		"PNPM_HOME="+filepath.Join(home, ".pnpm"),
		"CARGO_HOME="+filepath.Join(home, ".cargo"),
		"RUSTUP_HOME="+filepath.Join(home, ".rustup"),
		"PYTHONUSERBASE="+filepath.Join(home, ".local"),
		"PIP_CACHE_DIR="+filepath.Join(home, ".pip-cache"),
	)
	w.env = env
	return nil
}

// secretEnvRE matches environment variable names that carry credentials. A
// denylist rather than an allowlist: sandboxed sessions are meant to inherit the
// host's environment and binaries, so the goal is to strip secrets, not to
// curate a minimal env.
var secretEnvRE = regexp.MustCompile(`(?i)(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|AUTH|SESSION|COOKIE|LICENSE)`)

// envNameAllowed keeps a few names the pattern would otherwise catch but which
// carry no secret and break tooling when missing.
var envNameAllowed = map[string]bool{
	"KEYBOARD": true, "AUTHOR": true, "GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true,
}

// withoutSecrets drops credential-bearing variables from an environment slice.
func withoutSecrets(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok && !envNameAllowed[strings.ToUpper(name)] && secretEnvRE.MatchString(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// sudoRE matches a `sudo` (or `doas`) invocation: at the start of the command
// or right after a shell separator. Used to block privilege escalation in
// sandboxed sessions.
var sudoRE = regexp.MustCompile(`(^|[\s;&|(])(sudo|doas)\b`)

// blockedCmd returns a message to hand back to the model instead of running the
// command, or "" to allow it. Only enforced in sandboxed sessions. The model is
// told why and what to do instead, so it self-corrects rather than retrying.
func (w *workspace) blockedCmd(command string) string {
	if w.env == nil { // not sandboxed: no restrictions
		return ""
	}
	if sudoRE.MatchString(command) {
		return "[blocked] sudo/doas is not allowed in this sandboxed session and you never need it: HOME, PATH, and every package-manager prefix already point inside the writable workspace. Install and write there (e.g. under $DROSS_TOOLS), no root required."
	}
	return ""
}

// grep searches file contents under path (or the whole workspace) for a regexp,
// returning matches as "rel:line: text". Skips VCS/dep dirs and binary/large
// files, and caps the result.
// grepOpts are the optional narrowings and widenings of a search.
//
// Context exists because a bare matching line is rarely the answer: a search
// for a symbol was always followed by a read of the file it was found in, so
// the search cost two round trips instead of one. FilesOnly is the opposite
// need — "which files mention this" — where the lines themselves are noise.
type grepOpts struct {
	Path      string
	Context   int
	Glob      string
	FilesOnly bool
}

// maxGrepContext bounds how much a single search can quote back. Enough to see
// a function around a match, not enough to return a file a line at a time.
const maxGrepContext = 20

func (w *workspace) grep(pattern string, opts grepOpts) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("bad pattern: %w", err)
	}
	var pathRE *regexp.Regexp
	if opts.Glob != "" {
		pathRE, err = globToRegex(opts.Glob)
		if err != nil {
			return "", fmt.Errorf("bad glob: %w", err)
		}
	}
	if opts.Context > maxGrepContext {
		opts.Context = maxGrepContext
	}
	if opts.Context < 0 {
		opts.Context = 0
	}
	base := w.root
	if opts.Path != "" {
		abs, err := w.resolve(opts.Path)
		if err != nil {
			return "", err
		}
		base = abs
	}
	const maxMatches = 300
	var out strings.Builder
	matches := 0
	var files []string
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
		rel, _ := filepath.Rel(w.root, p)
		if pathRE != nil && !pathRE.MatchString(rel) {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() > 1<<20 {
			return nil // skip large files
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var lines []string
		binary := false
		for sc.Scan() {
			line := sc.Text()
			if !utf8.ValidString(line) {
				binary = true
				break
			}
			lines = append(lines, line)
		}
		if binary {
			return nil
		}

		hits := 0
		lastPrinted := 0 // 1-based; 0 means nothing printed from this file yet
		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			hits++
			matches++
			if opts.FilesOnly {
				break
			}
			if opts.Context == 0 {
				fmt.Fprintf(&out, "%s:%d: %s\n", rel, i+1, truncate(strings.TrimSpace(line), 300))
			} else {
				lastPrinted = writeWithContext(&out, rel, lines, i, opts.Context, lastPrinted)
			}
			if matches >= maxMatches {
				break
			}
		}
		if hits > 0 && opts.FilesOnly {
			files = append(files, rel)
		}
		return nil
	})
	if walkErr != nil && walkErr != io.EOF {
		return "", walkErr
	}
	if matches == 0 {
		return "[no matches]", nil
	}
	if opts.FilesOnly {
		sort.Strings(files)
		return strings.Join(files, "\n") + "\n", nil
	}
	res := out.String()
	if matches >= maxMatches {
		res += fmt.Sprintf("[truncated at %d matches]\n", maxMatches)
	}
	return res, nil
}

// writeWithContext prints the lines around a match, numbered, skipping anything
// already printed for an earlier match so overlapping windows do not repeat
// themselves. Returns the new high-water line number (1-based).
func writeWithContext(out *strings.Builder, rel string, lines []string, idx, ctx, lastPrinted int) int {
	start := idx - ctx
	if start < 0 {
		start = 0
	}
	end := idx + ctx
	if end > len(lines)-1 {
		end = len(lines) - 1
	}
	if start+1 <= lastPrinted {
		start = lastPrinted // already printed up to here
	} else if lastPrinted > 0 {
		out.WriteString("--\n")
	}
	if lastPrinted == 0 {
		fmt.Fprintf(out, "%s:\n", rel)
	}
	for i := start; i <= end; i++ {
		fmt.Fprintf(out, "%6d\t%s\n", i+1, truncate(lines[i], 300))
	}
	return end + 1
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
	case ".git", "node_modules", "vendor", ".venv", "dist", "build", sandboxDir:
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
	if !w.confined {
		// Unsandboxed: the agent may read and write wherever the person running
		// it can. Relative paths still resolve against the workspace, so the
		// common case is unchanged; an absolute one is simply honoured.
		return abs, nil
	}
	// Clean already collapsed .. segments; verify the result is under root.
	rel, err := filepath.Rel(w.root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace; this session is sandboxed", p)
	}
	// The lexical check above is not enough: a symlink inside the workspace can
	// point anywhere, and every file call would follow it. A repo can carry one,
	// so this needs no shell access to plant. Resolve what exists and re-check.
	if err := w.checkNoEscapeViaSymlink(abs); err != nil {
		return "", err
	}
	return abs, nil
}

// checkNoEscapeViaSymlink verifies that abs, with symlinks resolved, is still
// inside the workspace. The final element may not exist yet (a file being
// created), so the deepest existing ancestor is what gets resolved.
func (w *workspace) checkNoEscapeViaSymlink(abs string) error {
	realRoot, err := filepath.EvalSymlinks(w.root)
	if err != nil {
		realRoot = w.root // root itself missing; the lexical check stands
	}
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			rel, rerr := filepath.Rel(realRoot, real)
			if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("path escapes the workspace via a symlink")
			}
			return nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil // walked to the filesystem root without finding anything
		}
		probe = parent
	}
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

// maxReadPaths bounds a single multi-file read. Enough to open a package, not
// enough to pull a repository into the context window by accident.
const maxReadPaths = 20

// readFiles reads several files in one call, each headed by its path.
//
// This exists because concurrency was the wrong shape of answer for the models
// that need it most. A turn's read-only calls already run at the same time, but
// that only pays when a model puts several of them in one message, and a small
// local model does not: gpt-oss:20b was measured at almost exactly one tool
// call per turn, and went on issuing them one at a time even when the prompt
// told it in as many words to send all three together. Opening four files cost
// four round trips.
//
// So the batching moves into the call. A model that will not send three
// requests can still send one that asks for three things.
//
// A file that cannot be read is reported in place rather than failing the call,
// because the other three were fine and re-asking for them is the round trip
// this is trying to avoid.
func (w *workspace) readFiles(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("no path given")
	}
	if len(paths) > maxReadPaths {
		return "", fmt.Errorf("asked for %d files at once; the limit is %d — read the ones you need",
			len(paths), maxReadPaths)
	}
	if len(paths) == 1 {
		return w.readFile(paths[0])
	}
	var b strings.Builder
	failed := 0
	for _, p := range paths {
		out, err := w.readFile(p)
		fmt.Fprintf(&b, "=== %s ===\n", p)
		if err != nil {
			failed++
			fmt.Fprintf(&b, "[could not read: %s]\n\n", err)
			continue
		}
		b.WriteString(out)
		b.WriteString("\n")
	}
	if failed == len(paths) {
		// Nothing came back; that is a failed call, not a result with notes.
		return "", fmt.Errorf("none of the %d paths could be read:\n%s", len(paths), b.String())
	}
	return b.String(), nil
}

// rawFile returns a file's bytes as they are, with no line numbers, for the
// callers that need to diff two versions of it.
//
// readFile is for the model and numbers every line, which is right there and
// wrong here: diffing numbered text against raw text reports the whole file as
// changed, and diffing numbered against numbered puts line numbers inside the
// diff. A missing file is "" and no error — that is the "before" of a create.
func (w *workspace) rawFile(path string) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
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

// edit is one replacement within a file.
type edit struct {
	OldStr     string `json:"old_str"`
	NewStr     string `json:"new_str"`
	ReplaceAll bool   `json:"replace_all"`

	// Which file this entry changes, when the batch spans more than one.
	// Empty means the call's own path, so the single-file form is unchanged.
	Path string `json:"path"`
}

// editFile does an exact-string replacement that must match exactly once.
// Ambiguous (>1) or absent (0) matches are errors with a legible message — the
// quality of this one tool disproportionately shapes how good the agent feels.
func (w *workspace) editFile(path, oldStr, newStr string) (string, error) {
	return w.applyEdits(path, []edit{{OldStr: oldStr, NewStr: newStr}})
}

// applyEdits applies a list of replacements to one file, all or nothing.
//
// All or nothing because a half-applied batch is the worst outcome available:
// the file is in a state neither the model nor the user asked for, and the
// model's next move is based on a diff it did not make. Applying in order
// against the running text also means a later edit can legitimately touch what
// an earlier one produced.
// maxEditFiles bounds one multi-file edit, for the same reason maxReadPaths
// bounds a multi-file read: enough to finish a rename across a package, not
// enough to rewrite a repository from one call nobody reviewed.
const maxEditFiles = 20

// applyEditsAcross runs a batch whose entries may name different files.
//
// A rename is one change to a codebase and several changes to files, and until
// now the tool could only say the second thing: every file cost its own turn.
// Traced on rename-symbol, gpt-oss:20b spent turns 5, 6 and 9 on three edits
// that were one intention, and Claude Code — which has the same one-file-per-
// call shape — spends its turns the same way. read already took a list of
// paths and that is what cut its turns; this is the same move for the other
// side of the job.
//
// All or nothing across every file. A half-applied rename does not compile, and
// leaving one behind is worse than refusing the batch: the model can see a
// refusal, whereas a codebase that builds nowhere takes a turn to diagnose. So
// every file is edited in memory first, and nothing is written until all of
// them have succeeded.
func (w *workspace) applyEditsAcross(defaultPath string, edits []edit) (string, error) {
	if len(edits) == 0 {
		return "", fmt.Errorf("no edits given")
	}
	// Grouped in first-seen order so the report reads in the order the model
	// asked for, and so a file named twice is one group rather than two.
	var order []string
	byPath := map[string][]edit{}
	for i, e := range edits {
		p := e.Path
		if p == "" {
			p = defaultPath
		}
		if p == "" {
			return "", fmt.Errorf("edit %d has no path: give the call a path, or a path on each entry "+
				"when the batch spans several files", i+1)
		}
		if _, seen := byPath[p]; !seen {
			order = append(order, p)
		}
		byPath[p] = append(byPath[p], e)
	}
	if len(order) == 1 {
		return w.applyEdits(order[0], byPath[order[0]])
	}
	if len(order) > maxEditFiles {
		return "", fmt.Errorf("asked to edit %d files at once; the limit is %d", len(order), maxEditFiles)
	}

	// Dry run everything before writing anything.
	staged := make(map[string]string, len(order))
	var reports []string
	for _, p := range order {
		out, err := w.editInMemory(p, byPath[p])
		if err != nil {
			return "", fmt.Errorf("%s (nothing was written; the batch changes %d files and all of them "+
				"must apply)", err, len(order))
		}
		staged[p] = out.text
		reports = append(reports, out.report)
	}
	for _, p := range order {
		abs, err := w.resolve(p)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, []byte(staged[p]), 0o644); err != nil {
			return "", fmt.Errorf("%s: %w", p, err)
		}
	}
	return strings.Join(reports, "\n\n"), nil
}

func (w *workspace) applyEdits(path string, edits []edit) (string, error) {
	out, err := w.editInMemory(path, edits)
	if err != nil {
		return "", err
	}
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(out.text), 0o644); err != nil {
		return "", err
	}
	return out.report, nil
}

// staged is one file's edited contents and the line to report for it, computed
// without touching the disk so a multi-file batch can check every file before
// committing to any of them.
type staged struct {
	text   string
	report string
}

func (w *workspace) editInMemory(path string, edits []edit) (staged, error) {
	if len(edits) == 0 {
		return staged{}, fmt.Errorf("no edits given")
	}
	abs, err := w.resolve(path)
	if err != nil {
		return staged{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return staged{}, err
	}

	text := string(data)
	total, adjusted, skipped := 0, 0, 0
	for i, e := range edits {
		if e.OldStr == "" {
			return staged{}, fmt.Errorf("edit %d has an empty old_str. To replace text, pass old_str and "+
				"new_str, or an array under the key `edits`; to create a file, use write", i+1)
		}
		// A single entry that changes nothing used to reject the whole batch,
		// which cost a turn to be told something that changed no file. When a
		// model lists every occurrence of a symbol, one of them being already
		// correct is ordinary; skip it and do the rest. A batch where nothing
		// would change is still an error, because that is a model that thinks
		// it is working and is not.
		if e.OldStr == e.NewStr {
			skipped++
			continue
		}
		n := strings.Count(text, e.OldStr)
		if n == 0 {
			// Nothing matched byte for byte. Before failing, try again ignoring
			// each line's leading whitespace — see reindentedFor.
			if updated, ok := looseReplace(text, e.OldStr, e.NewStr); ok {
				text = updated
				total++
				adjusted++
				continue
			}
			return staged{}, fmt.Errorf("%s", noMatchMessage(path, text, e.OldStr, i, len(edits)))
		}
		if n > 1 && !e.ReplaceAll {
			return staged{}, fmt.Errorf("%s", ambiguousMessage(path, text, e.OldStr, n, i, len(edits)))
		}
		if e.ReplaceAll {
			text = strings.ReplaceAll(text, e.OldStr, e.NewStr)
			total += n
			continue
		}
		text = strings.Replace(text, e.OldStr, e.NewStr, 1)
		total++
	}

	if total == 0 {
		return staged{}, fmt.Errorf("nothing to do: %s already reads the way every edit asks for. "+
			"Check the file before editing it again", path)
	}

	// Hand back the result rather than only the fact of it: a model that cannot
	// see what it produced re-reads the file to check, which costs a whole
	// round trip to learn something this call already knows.
	var notes []string
	if adjusted > 0 {
		// Never silent: the edit that landed is not byte-for-byte the one that
		// was asked for, and the echoed region below shows what it became.
		notes = append(notes, fmt.Sprintf("%d matched only after ignoring indentation, "+
			"and used the file's own", adjusted))
	}
	if skipped > 0 {
		notes = append(notes, fmt.Sprintf("%d already read that way and were skipped", skipped))
	}
	note := ""
	if len(notes) > 0 {
		note = " — " + strings.Join(notes, "; ")
	}
	return staged{
		text: text,
		report: fmt.Sprintf("edited %s (%d replacement%s%s)\n\n%s",
			path, total, plural(total), note, changedRegion(string(data), text)),
	}, nil
}

// looseReplace applies an edit whose old_str matches the file except for the
// whitespace at the start of its lines.
//
// A model reconstructing indentation from a line-numbered read is guessing:
// read separates the number from the content with a tab, the content of a Go
// file starts with tabs, and the boundary between the two is not visible. A run
// was watched failing exactly there — the model dropped one leading tab, the
// edit was refused, it retried with four spaces, and gave up having changed
// nothing. Refusing that edit is technically correct and practically useless:
// the intent was unambiguous.
//
// So it is honoured, but only when it is genuinely unambiguous — the trimmed
// lines match in exactly one place — and the file's own indentation is kept
// rather than the model's guess. Anything less certain still fails, loudly.
func looseReplace(text, oldStr, newStr string) (string, bool) {
	fileLines := strings.Split(text, "\n")
	oldLines := strings.Split(oldStr, "\n")
	if len(oldLines) == 0 || len(oldLines) > len(fileLines) {
		return "", false
	}
	trimmedEqual := func(a, b string) bool {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}

	at := -1
	for i := 0; i+len(oldLines) <= len(fileLines); i++ {
		match := true
		for j := range oldLines {
			if !trimmedEqual(fileLines[i+j], oldLines[j]) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if at >= 0 {
			return "", false // more than one place: not unambiguous
		}
		at = i
	}
	if at < 0 {
		return "", false
	}

	region := fileLines[at : at+len(oldLines)]
	out := append([]string{}, fileLines[:at]...)
	out = append(out, reindentedFor(newStr, region)...)
	out = append(out, fileLines[at+len(oldLines):]...)
	return strings.Join(out, "\n"), true
}

// reindentedFor puts the replacement lines back in the file's indentation.
//
// When the replacement has one line per replaced line — the common case, a
// changed word or a changed return — each keeps the indentation of the line it
// stands in for, which is exactly right whatever the model sent. Otherwise the
// shape has changed and there is nothing to map onto, so every line is shifted
// by the indentation the first line was missing.
func reindentedFor(newStr string, region []string) []string {
	newLines := strings.Split(newStr, "\n")
	out := make([]string, 0, len(newLines))
	if len(newLines) == len(region) {
		for i, l := range newLines {
			if strings.TrimSpace(l) == "" {
				out = append(out, l) // a blank line has no indentation to keep
				continue
			}
			out = append(out, leadingWS(region[i])+strings.TrimLeft(l, " \t"))
		}
		return out
	}
	shift := leadingWS(region[0])
	for _, l := range newLines {
		if strings.TrimSpace(l) == "" {
			out = append(out, l)
			continue
		}
		out = append(out, shift+strings.TrimLeft(l, " \t"))
	}
	return out
}

func leadingWS(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// editWhere labels which edit of a batch failed, and says nothing at all for a
// single edit, where "edit 1 of 1" is just noise.
func editWhere(i, of int) string {
	if of <= 1 {
		return ""
	}
	return fmt.Sprintf(" (edit %d of %d; nothing was written)", i+1, of)
}

// noMatchMessage explains a failed match, and where it can, supplies the fix.
//
// "It must match exactly including whitespace" is true and nearly useless: the
// model already believes it matched exactly. The common cause is indentation —
// tabs against spaces, or a level of nesting misremembered — so when the text
// is there under different whitespace, say so and quote the real line. That
// turns the most expensive failure in the agent, a re-read followed by an
// identical retry, into a correction the model can make from this message
// alone.
func noMatchMessage(path, text, oldStr string, i, of int) string {
	msg := fmt.Sprintf("old_str not found in %s%s", path, editWhere(i, of))
	// An old_str that differs only by indentation has already been applied by
	// now, so reaching here means the content differs too. Anchor on the first
	// line and show the region the model was probably aiming at: quoting one
	// line of a three-line old_str would leave it reconstructing the rest from
	// the same memory that just failed it.
	if _, n, ok := findIgnoringIndent(text, oldStr); ok {
		want := strings.Count(oldStr, "\n") + 1
		return fmt.Sprintf("%s. It starts at line %d, but the rest does not match. The file has:\n%s\n"+
			"Copy that block exactly, including leading whitespace, and change only what you mean to.",
			msg, n, quoteLines(regionAt(text, n, want)))
	}
	if strings.Contains(text, strings.TrimSpace(oldStr)) {
		return msg + " — but a trimmed version of old_str does appear. The surrounding" +
			" whitespace differs; read the file and copy the region verbatim."
	}
	// Nothing of it is here at all. Saying so stops the retry: a rename that
	// walks the wrong file otherwise fails once for the whitespace it assumes
	// is to blame, and again for the same reason.
	if word := firstWord(oldStr); word != "" && !strings.Contains(text, word) {
		return fmt.Sprintf("%s — and %q does not appear anywhere in this file. "+
			"You are editing the wrong file; grep for it to find the right one.", msg, word)
	}
	return msg + "; it must match the file exactly, including whitespace. Read the file and copy the region."
}

// commonWords are the words whose absence from a file says nothing. Reporting
// that "return" is missing would be noise; reporting that "Clamp" is would be
// the answer.
var commonWords = map[string]bool{
	"return": true, "func": true, "for": true, "range": true, "int": true,
	"string": true, "bool": true, "err": true, "error": true, "nil": true,
	"var": true, "const": true, "type": true, "struct": true, "package": true,
	"import": true, "true": true, "false": true, "the": true, "and": true,
}

// firstWord picks the most distinctive identifier in s.
//
// An identifier carrying an uppercase letter wins outright — it is a name
// somebody chose, so its absence localises the mistake. Failing that, the
// longest word that is not boilerplate.
func firstWord(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
	best := ""
	for _, w := range words {
		if len(w) < 3 || commonWords[strings.ToLower(w)] {
			continue
		}
		named := w != strings.ToLower(w)
		bestNamed := best != "" && best != strings.ToLower(best)
		switch {
		case named && !bestNamed:
			best = w // a chosen name beats any plain one
		case named == bestNamed && len(w) > len(best):
			best = w
		}
	}
	return best
}

// ambiguousMessage names the sites instead of asking the model to go and find
// them. Without this the model re-reads the whole file to build a unique
// old_str, which is a turn spent recovering information this call already had.
func ambiguousMessage(path, text, oldStr string, n, i, of int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "old_str matched %d times in %s%s. Either pass replace_all to change every one,"+
		" or use one of these instead — each already includes enough context to match only its own site:\n",
		n, path, editWhere(i, of))
	shown := 0
	for _, c := range uniqueCandidates(text, oldStr, 3) {
		fmt.Fprintf(&b, "\n%s:%d\n%s\n", path, c.num, c.text)
		shown++
	}
	if shown == 0 {
		// Every site expanded to the whole file without becoming unique, which
		// means the file really does repeat itself. Naming the lines is all
		// there is to offer.
		b.Reset()
		fmt.Fprintf(&b, "old_str matched %d times in %s%s. Either pass replace_all to change every one,"+
			" or add surrounding context so it is unique. It matches at:\n", n, path, editWhere(i, of))
		for _, ln := range matchLines(text, oldStr, 5) {
			fmt.Fprintf(&b, "  %s:%d: %s\n", path, ln.num, strings.TrimSpace(ln.text))
		}
	}
	if n > shown && shown > 0 {
		fmt.Fprintf(&b, "\n…and %d more site(s).\n", n-shown)
	}
	return strings.TrimRight(b.String(), "\n")
}

// candidate is one site's ready-made replacement for an ambiguous old_str.
type candidate struct {
	num  int
	text string
}

// uniqueCandidates expands an ambiguous old_str, at each of the first max
// sites, into the smallest whole-line window around it that occurs exactly
// once.
//
// Naming the line numbers was not enough on its own. Watched on find-the-bug:
// `old_str: "return hi"` matched twice, and the model answered the "add
// surrounding context" advice by re-reading the file, re-reading it a second
// time identically, and then sending an edits array whose first entry replaced
// "return hi" with itself — four turns to build a string this call could have
// handed it. The information was already here; only the message was missing.
func uniqueCandidates(text, oldStr string, max int) []candidate {
	var out []candidate
	offset := 0
	for len(out) < max {
		i := strings.Index(text[offset:], oldStr)
		if i < 0 {
			break
		}
		at := offset + i
		if win, ok := uniqueWindow(text, at, at+len(oldStr)); ok {
			out = append(out, candidate{num: strings.Count(text[:at], "\n") + 1, text: win})
		}
		offset = at + len(oldStr)
	}
	return out
}

// maxWindowGrowth bounds how far a candidate grows looking for uniqueness. A
// window that has swallowed this many lines either side is no longer a useful
// thing to paste into old_str, and replace_all is the better answer.
const maxWindowGrowth = 6

// uniqueWindow grows [start,end) a line at a time until the text it spans
// appears exactly once.
//
// Upward to exhaustion before downward, rather than alternating. What tells two
// identical lines apart is nearly always what comes before them — the `if` whose
// body they are — and a window that reaches forward as well is a larger thing to
// paste for no gain: growing both directions on "return hi" produced a window
// carrying the line after it too, which matched just as uniquely and read as
// more than was asked for.
func uniqueWindow(text string, start, end int) (string, bool) {
	up := start
	for i := 0; i < maxWindowGrowth && up > 0; i++ {
		up = strings.LastIndexByte(text[:up-1], '\n') + 1
		if s := text[up:end]; strings.Count(text, s) == 1 {
			return s, true
		}
	}
	// Nothing above it is distinguishing, so take what is below as well.
	for i := 0; i < maxWindowGrowth && end < len(text); i++ {
		if nl := strings.IndexByte(text[end:], '\n'); nl < 0 {
			end = len(text)
		} else {
			end += nl + 1
		}
		if s := text[up:end]; strings.Count(text, s) == 1 {
			return s, true
		}
	}
	return "", false
}

type matchLine struct {
	num  int
	text string
}

// matchLines reports the first max lines on which oldStr's matches begin.
func matchLines(text, oldStr string, max int) []matchLine {
	var out []matchLine
	offset := 0
	for len(out) < max {
		i := strings.Index(text[offset:], oldStr)
		if i < 0 {
			break
		}
		at := offset + i
		num := strings.Count(text[:at], "\n") + 1
		start := strings.LastIndexByte(text[:at], '\n') + 1
		end := strings.IndexByte(text[at:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += at
		}
		out = append(out, matchLine{num: num, text: text[start:end]})
		offset = at + len(oldStr)
	}
	return out
}

// findIgnoringIndent looks for oldStr's first line with its leading whitespace
// ignored, and returns the file's own version of that line.
func findIgnoringIndent(text, oldStr string) (line string, num int, ok bool) {
	want := strings.TrimSpace(firstNonBlankLine(oldStr))
	if want == "" {
		return "", 0, false
	}
	for i, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == want {
			return l, i + 1, true
		}
	}
	return "", 0, false
}

func firstNonBlankLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// regionAt returns count lines of the file starting at the 1-based line n.
func regionAt(text string, n, count int) string {
	lines := strings.Split(text, "\n")
	start := n - 1
	if start < 0 {
		start = 0
	}
	end := start + count
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}

// quoteLines renders text in a fenced block so leading whitespace survives the
// trip through the model's own formatting.
func quoteLines(s string) string {
	return "```\n" + s + "\n```"
}

// editContext is how many lines either side of a change are echoed back.
const editContext = 4

// changedRegion returns the numbered lines around the first difference between
// two versions of a file, so an edit reports what it produced.
func changedRegion(before, after string) string {
	oldLines := strings.Split(before, "\n")
	newLines := strings.Split(after, "\n")
	first := 0
	for first < len(oldLines) && first < len(newLines) && oldLines[first] == newLines[first] {
		first++
	}
	if first >= len(newLines) {
		return ""
	}
	// How far the change runs, measured from the end so an insertion does not
	// look like it rewrote everything below it.
	lastOld, lastNew := len(oldLines)-1, len(newLines)-1
	for lastOld > first && lastNew > first && oldLines[lastOld] == newLines[lastNew] {
		lastOld--
		lastNew--
	}
	start := first - editContext
	if start < 0 {
		start = 0
	}
	end := lastNew + editContext
	if end > len(newLines)-1 {
		end = len(newLines) - 1
	}
	var b strings.Builder
	b.WriteString("The file now reads:\n```\n")
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, newLines[i])
	}
	b.WriteString("```")
	return b.String()
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
	if msg := w.blockedCmd(command); msg != "" {
		return msg, nil
	}
	ctx, cancel := contextWithTimeout(timeout)
	defer cancel()
	start := time.Now()
	checks := 0
	for {
		checks++
		argv := w.sandboxArgv([]string{"bash", "-c", command}, nil)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = w.root
		if w.env != nil {
			cmd.Env = w.env
		}
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
	if msg := w.blockedCmd(command); msg != "" {
		return msg, nil
	}
	ctx, cancel := contextWithTimeout(bashTimeout)
	defer cancel()

	argv := w.sandboxArgv([]string{"bash", "-c", command}, nil)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = w.root
	if w.env != nil {
		cmd.Env = w.env
	}
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

// editedPaths lists the files a batch will touch, in first-seen order, so the
// caller can read each one before and after without re-deriving the grouping.
func editedPaths(defaultPath string, edits []edit) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range edits {
		p := e.Path
		if p == "" {
			p = defaultPath
		}
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// todoItem is one step of the plan a run is working through.
//
// `task` is what dross calls it; `content` is what a model trained on other
// harnesses reaches for. Accepting both costs a struct field and saves the turn
// that a rejected list would cost.
type todoItem struct {
	Task    string `json:"task"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

func (t todoItem) text() string {
	if t.Task != "" {
		return t.Task
	}
	return t.Content
}

// normalizedStatus maps what models actually write onto the three states.
//
// "done" and "complete" for completed, "doing"/"active"/"current" for
// in_progress. A status nobody can parse is treated as pending rather than
// rejected: the list is a note to self, and refusing to keep it because of one
// word costs a turn and helps nobody.
func (t todoItem) normalizedStatus() string {
	switch strings.ToLower(strings.TrimSpace(t.Status)) {
	case "completed", "complete", "done", "finished", "[x]", "x":
		return "completed"
	case "in_progress", "in-progress", "inprogress", "doing", "active", "current", "started":
		return "in_progress"
	default:
		return "pending"
	}
}
