package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
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
	// scratch is this run's temp directory, the one path outside root a
	// confined session may write to. Empty means no scratchpad was configured
	// and the exception does not apply.
	scratch string
	// extra are additional working directories, absolute. They get the same
	// tool access as root: readable, writable, searched. Claude Code draws the
	// line in the same place -- a directory is allowed or it is not, with no
	// read-only middle state to reason about.
	//
	// root stays primary. Git status, the workspace map and the orientation
	// text all describe root alone, because "which project is this" has one
	// answer even when three trees are open.
	extra []string
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
	Path       string
	Context    int
	Glob       string
	FilesOnly  bool
	IgnoreCase bool
}

// anyDepth makes a glob with no slash in it mean what people mean by it.
//
// "*.go" compiles to ^[^/]*\.go$, which matches main.go and nothing else — in
// any repository with directories, the answer to `glob("*.go")` was "[no
// files]". A model reads that as "there are no Go files here" and stops looking,
// which is one of the ways a run ends up insisting the code it was asked about
// does not exist.
//
// A pattern with a slash in it is a path and is left exactly alone: "cmd/*.go"
// means that directory, not every directory. "./*.go" is how to say the root
// and only the root, which is the meaning a bare "*.go" used to carry and the
// one case this rule would otherwise make unsayable.
func anyDepth(pattern string) string {
	if rooted := strings.TrimPrefix(pattern, "./"); rooted != pattern {
		return rooted
	}
	if pattern == "" || strings.Contains(pattern, "/") {
		return pattern
	}
	return "**/" + pattern
}

// literalOrRegex compiles pattern, falling back to a literal search when it is
// not valid regular-expression syntax.
//
// Searching for "Rate()" or "map[string]any" is an ordinary thing to want, and
// both are broken regexps. Returning "bad pattern: missing closing )" spends a
// turn teaching the model to escape things, when what it asked for has an
// obvious reading. The note says what happened, so a model that did mean the
// regexp can fix it.
func literalOrRegex(pattern string, ignoreCase bool) (*regexp.Regexp, string, error) {
	prefix := ""
	if ignoreCase {
		prefix = "(?i)"
	}
	re, err := regexp.Compile(prefix + pattern)
	if err == nil {
		return re, "", nil
	}
	lit, litErr := regexp.Compile(prefix + regexp.QuoteMeta(pattern))
	if litErr != nil {
		return nil, "", fmt.Errorf("bad pattern: %w", err)
	}
	return lit, fmt.Sprintf("[%q is not valid regexp syntax (%v), so it was searched for literally]\n",
		pattern, err), nil
}

// maxGrepContext bounds how much a single search can quote back. Enough to see
// a function around a match, not enough to return a file a line at a time.
const maxGrepContext = 20

// grep searches file contents, and when it finds nothing it says what would
// have found something.
//
// A search that comes back "[no matches]" is where a run goes wrong: the model
// has asked the one question it knows how to ask, been told no, and has nothing
// to try next. Often the answer was there and the case was different, so the
// no-match path re-runs case-insensitively and hands over those results rather
// than the dead end. One extra walk, only on failure, against a turn spent
// concluding the symbol does not exist.
func (w *workspace) grep(pattern string, opts grepOpts) (string, error) {
	out, matches, err := w.grepOnce(pattern, opts)
	if err != nil || matches > 0 || opts.IgnoreCase {
		return out, err
	}
	// Nothing, and case was not the thing being tested. Try it.
	insensitive := opts
	insensitive.IgnoreCase = true
	retry, n, retryErr := w.grepOnce(pattern, insensitive)
	if retryErr != nil || n == 0 {
		return w.noMatchAdvice(pattern, opts), nil
	}
	return fmt.Sprintf("[no matches for %q as written; %d found ignoring case, shown below]\n%s",
		pattern, n, retry), nil
}

// noMatchAdvice turns a dead end into a next move.
//
// Which move depends on why it failed, and the one that matters is a glob that
// matched no files at all: the pattern was never the problem, and a model told
// only "[no matches]" will rewrite the pattern forever.
func (w *workspace) noMatchAdvice(pattern string, opts grepOpts) string {
	if opts.Glob != "" {
		if files, err := w.globCount(opts.Glob); err == nil && files == 0 {
			return fmt.Sprintf("[no matches: the glob %q matched no files, so nothing was searched. "+
				"Check the glob before the pattern — glob(%q) lists what it selects.]", opts.Glob, opts.Glob)
		}
	}
	return fmt.Sprintf("[no matches for %q, with or without case. "+
		"Try a shorter or more distinctive fragment of it, or glob for the file first.]", pattern)
}

// globCount reports how many files a glob selects, for diagnosing a search that
// searched nothing.
func (w *workspace) globCount(pattern string) (int, error) {
	re, err := globToRegex(anyDepth(pattern))
	if err != nil {
		return 0, err
	}
	n := 0
	err = filepath.WalkDir(w.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) && p != w.root {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(w.root, p); err == nil && re.MatchString(rel) {
			n++
		}
		return nil
	})
	return n, err
}

// grepOnce is one pass, returning the rendered output and how many matches it
// found.
func (w *workspace) grepOnce(pattern string, opts grepOpts) (string, int, error) {
	re, note, err := literalOrRegex(pattern, opts.IgnoreCase)
	if err != nil {
		return "", 0, err
	}
	var pathRE *regexp.Regexp
	if opts.Glob != "" {
		pathRE, err = globToRegex(anyDepth(opts.Glob))
		if err != nil {
			return "", 0, fmt.Errorf("bad glob: %w", err)
		}
	}
	if opts.Context > maxGrepContext {
		opts.Context = maxGrepContext
	}
	if opts.Context < 0 {
		opts.Context = 0
	}
	bases := w.roots()
	if opts.Path != "" {
		abs, err := w.resolve(opts.Path)
		if err != nil {
			return "", 0, err
		}
		bases = []string{abs}
	}
	const maxMatches = 300
	var out strings.Builder
	matches := 0
	var files []string
	walk := func(base string) error {
		return filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
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
			rel := w.label(p)
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
	}
	var walkErr error
	for _, base := range bases {
		if err := walk(base); err != nil {
			walkErr = err
			break
		}
	}
	if walkErr != nil && walkErr != io.EOF {
		return "", 0, walkErr
	}
	if matches == 0 {
		return "", 0, nil
	}
	if opts.FilesOnly {
		sort.Strings(files)
		return note + strings.Join(files, "\n") + "\n", matches, nil
	}
	res := note + out.String()
	if matches >= maxMatches {
		res += fmt.Sprintf("[truncated at %d matches]\n", maxMatches)
	}
	return res, matches, nil
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
//
// A pattern with no slash matches at any depth — see anyDepth — because
// "*.go" meaning "Go files in the root and nowhere else" is a trap rather than
// a feature.
func (w *workspace) glob(pattern string) (string, error) {
	re, err := globToRegex(anyDepth(pattern))
	if err != nil {
		return "", err
	}
	var out []string
	for _, base := range w.roots() {
		root := base
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDir(d.Name()) && p != root {
					return filepath.SkipDir
				}
				return nil
			}
			// Matched on the path as it will be reported, so a pattern that
			// mentions a directory works the same in an added tree.
			name := w.label(p)
			if re.MatchString(name) || re.MatchString(mustRel(root, p)) {
				out = append(out, name)
			}
			return nil
		})
	}
	if len(out) == 0 {
		return fmt.Sprintf("[no files match %q. Patterns are matched against the whole path, "+
			"so try a bare name like *_test.go, or a fragment of the directory you expect.]", pattern), nil
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

// label names a file the way the model should refer to it: relative inside the
// primary workspace, which is what every existing prompt and tool result looks
// like, and absolute anywhere else.
//
// A path relative to root would name a different file -- or no file -- once
// there is a second tree, and the model would spend a turn discovering that.
func (w *workspace) label(p string) string {
	if rel, err := filepath.Rel(w.root, p); err == nil &&
		rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return p
}

func mustRel(base, p string) string {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return rel
}

// roots is every directory this session may work in, primary first.
func (w *workspace) roots() []string {
	return append([]string{w.root}, w.extra...)
}

// underAny reports whether abs sits inside any of dirs.
func underAny(dirs []string, abs string) bool {
	for _, d := range dirs {
		rel, err := filepath.Rel(d, abs)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

// scratchRelative reads "scratchpad/x.html" as the session scratchpad rather
// than a directory of that name in the repository.
//
// Told to put a mockup in the scratchpad and given its absolute path, a model
// wrote "scratchpad/dross_ui_designs.html" — a relative path, which landed in
// the user's checkout and left a directory they would have had to clean out.
// The prompt said not to. Saying it more firmly is the fix that has failed
// repeatedly here, so the path resolves to what was meant instead.
//
// Only when the workspace has no such directory of its own: a project that
// really keeps a scratchpad/ owns that name, and redirecting reads of it would
// be worse than the litter this avoids.
func (w *workspace) scratchRelative(p string) (string, bool) {
	if w.scratch == "" {
		return "", false
	}
	first, rest, ok := strings.Cut(filepath.ToSlash(filepath.Clean(p)), "/")
	if !ok || first != "scratchpad" || rest == "" {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(w.root, "scratchpad")); err == nil {
		return "", false // the project owns the name
	}
	abs := filepath.Join(w.scratch, filepath.FromSlash(rest))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", false
	}
	return abs, true
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
	} else if rel, ok := w.scratchRelative(p); ok {
		abs = rel
	} else {
		abs = filepath.Join(w.root, p)
	}
	if !w.confined {
		// Unsandboxed: the agent may read and write wherever the person running
		// it can. Relative paths still resolve against the workspace, so the
		// common case is unchanged; an absolute one is simply honoured.
		return abs, nil
	}
	// The scratchpad is the one place outside the workspace a sandboxed session
	// may write, and it is an exception rather than a widening: a single
	// directory this process was given, under a per-user, per-session path
	// nothing else writes to. Without it a sandboxed run has nowhere legal to
	// put a temp file and puts it in the repository instead.
	// An added working directory is as legitimate as the primary one; the
	// sandbox is about what this session may touch, not about how many trees
	// that is.
	if len(w.extra) > 0 && underAny(w.extra, abs) {
		return abs, nil
	}
	if underScratchpad(w.scratch, abs) {
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			return "", err
		}
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

// readWindow bounds a read to a range of lines.
//
// Without one, a file over maxReadBytes was a refusal that sent the model to
// bash for a sed one-liner: a round trip, and output with no line numbers on
// it, from a tool the run then has to be talked out of using for everything
// else. A window keeps the big file inside the tool that already knows how to
// present it — and makes line 2400 of a 3000-line file worth asking for
// without the 2399 in front of it.
type readWindow struct {
	// Offset is the first line to return, 1-based; 0 reads from the start.
	// Negative counts from the end — -200 is the last 200 lines.
	//
	// The negative form exists because the end of a file is not reachable any
	// other way: an offset has to be a line number, and you cannot know the
	// last line number of a file you have not read. A traced run given a 300 KB
	// log walked it from line 1 in 500-line slices, eight round trips, and ran
	// out of turns two hundred lines short of the FATAL that was the answer.
	// The end of a log is where its answer usually is, and it was the one place
	// the tool could not go.
	Offset int
	Limit  int // how many lines to return; 0 means as many as fit. Ignored when Offset is negative.
}

func (r readWindow) empty() bool { return r.Offset == 0 && r.Limit <= 0 }

// maxSliceLines caps a windowed read that named an offset and no limit. Long
// enough to be a real look at a file, short enough that "start at 1" on a
// 200,000-line log is not the whole log.
const maxSliceLines = 2000

// redirect is a tool declining a call and naming what to do instead.
//
// It is not a failure and must not be shown as one. Nothing has gone wrong: the
// model asked for something the tool will not do, was told exactly how to get
// it, and the next call is normally the right one — a traced run read a 300 KB
// log, was redirected, and had the answer one call later. Reported as an error
// that sequence puts a red cross in the transcript for a step that went to
// plan, which is the user reading a failure into a working run.
//
// The distinction is who the text is for. An error is for the person: something
// they may have to fix. A redirect is for the model: instructions it acts on
// without anyone needing to know.
type redirect struct{ error }

func redirectf(format string, a ...any) error {
	return redirect{fmt.Errorf(format, a...)}
}

// isRedirect reports whether err is guidance rather than a failure.
func isRedirect(err error) bool {
	var r redirect
	return errors.As(err, &r)
}

// maxScanBytes bounds what a windowed read will stream.
//
// A window skips the whole-file size check, which is the point of it, and for a
// while skipped every other bound with it. Reading a 3 GiB file with
// offset=1 limit=5 spent two and a half seconds pulling the entire thing
// through ReadString and held it in memory, because a file with no newline in
// it is one line and ReadString returns lines.
//
// 32 MiB is 128 times what a whole read returns and larger than any log worth
// paging through a screen at a time. Past that, grep and `bash tail` are the
// honest answers, and the refusal says so rather than trying.
const maxScanBytes = 32 << 20

// readFile returns line-numbered content so the model can reason about ranges.
func (w *workspace) readFile(path string, win readWindow) (string, error) {
	abs, err := w.resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !win.empty() {
		if info.Size() > maxScanBytes {
			return "", redirectf("%s is %d bytes; too large to page through (limit %d) — "+
				"grep it for what you want, or run `tail -n 200 %s` with bash for the end",
				path, info.Size(), maxScanBytes, path)
		}
		return w.readSlice(path, abs, win)
	}
	if info.Size() > maxReadBytes {
		// The order of these is the whole message. An earlier version led with
		// "read it in slices (offset 1 limit 500, then offset 501)" and a run
		// followed it literally: eight slices down a log whose answer was in
		// the last six lines. Search first, the end second, and the walk from
		// the top last, because it is last in usefulness.
		return "", redirectf("file is %d bytes; too large to read whole (limit %d) — grep it for what "+
			"you want, or read the end with offset -200 (a log's answer is usually at the end), or "+
			"walk it in slices (offset 1 limit 200, then offset 201)",
			info.Size(), maxReadBytes)
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

// readSlice returns the requested lines of a file, numbered as they are in it.
//
// It streams rather than reading the file in, because the files this exists for
// are the ones too big to hold. It keeps counting past the end of the slice so
// the footer can say how much is left and where to resume: a slice that stops
// without saying what follows it is how a run concludes it has seen a file it
// has seen a fifth of.
func (w *workspace) readSlice(rel, abs string, win readWindow) (string, error) {
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if win.Offset < 0 {
		return tailLines(rel, f, -win.Offset)
	}

	start := win.Offset
	if start <= 0 {
		start = 1
	}
	limit := win.Limit
	if limit <= 0 {
		limit = maxSliceLines
	}

	br := bufio.NewReader(f)
	var b strings.Builder
	total, shown := 0, 0
	full := false // the slice ran out of budget before it ran out of lines
	for {
		line, rerr := br.ReadString('\n')
		if line != "" || rerr == nil {
			total++
			if total >= start && shown < limit && !full {
				text := strings.TrimSuffix(line, "\n")
				switch {
				case b.Len()+len(text) <= maxReadBytes:
					fmt.Fprintf(&b, "%6d\t%s\n", total, text)
					shown++
				case shown == 0:
					// One line longer than the entire budget: a minified
					// bundle, a one-line JSON dump. Returning nothing for it
					// reported the file as shorter than it is — "offset 1 is
					// past the end of bundle.js, which has 3 lines", a sentence
					// that contradicts itself and stops the model asking again.
					// The prefix plus a note about the rest is the useful
					// answer, and it is what elide does with oversized output
					// everywhere else here.
					fmt.Fprintf(&b, "%6d\t%s\n[line %d truncated: %d of %d bytes shown]\n",
						total, text[:validPrefix(text, maxReadBytes-64)],
						total, validPrefix(text, maxReadBytes-64), len(text))
					shown++
					full = true
				default:
					full = true
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	if shown == 0 {
		switch {
		case total == 0:
			return "", fmt.Errorf("%s is empty", rel)
		case start > total:
			return "", fmt.Errorf("offset %d is past the end of %s, which has %d lines", start, rel, total)
		default:
			// The lines exist and none of them fit. Defensive: the truncating
			// branch above shows the first one whatever its size, so this is
			// unreachable — and an accurate error beats an inaccurate one if
			// that ever stops being true.
			return "", fmt.Errorf("no lines of %s fit the reply budget from offset %d", rel, start)
		}
	}

	next := start + shown
	if next <= total {
		reason := fmt.Sprintf("%d more lines (%d in the file)", total-next+1, total)
		if full {
			reason += fmt.Sprintf("; the slice stopped at the %d byte limit", maxReadBytes)
		}
		fmt.Fprintf(&b, "\n[… %s; continue with read(path=%q, offset=%d).]\n", reason, rel, next)
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
func (w *workspace) readFiles(paths []string, win readWindow) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("no path given")
	}
	if len(paths) > maxReadPaths {
		return "", fmt.Errorf("asked for %d files at once; the limit is %d — read the ones you need",
			len(paths), maxReadPaths)
	}
	if len(paths) == 1 {
		return w.readFile(paths[0], win)
	}
	var b strings.Builder
	failed := 0
	for _, p := range paths {
		out, err := w.readFile(p, win)
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

// validPrefix is the largest n <= max where s[:n] ends on a rune boundary.
//
// Cutting a line at a byte offset lands mid-rune about three times in four for
// non-ASCII text, and the replacement character that produces is indistinguishable
// from the file actually containing one.
func validPrefix(s string, max int) int {
	if max >= len(s) {
		return len(s)
	}
	if max < 0 {
		return 0
	}
	for max > 0 && !utf8.ValidString(s[:max]) {
		max--
	}
	return max
}

// tailLines returns the last want lines of r, numbered as they are in the file.
//
// A ring of want strings rather than the file: the whole point is that the file
// does not fit. The count is only known at the end, so the numbers are assigned
// after the scan, and the header says how much came before — a tail with no
// idea what it is the tail of leaves the run unable to ask for the rest.
func tailLines(rel string, r io.Reader, want int) (string, error) {
	if want <= 0 {
		return "", fmt.Errorf("asked for %d lines from the end of %s", want, rel)
	}
	// The ring is sized from the request, so an unbounded request sizes it
	// without reference to the file: offset -5000000 on a three-line file
	// allocated 76 MB, and the same arithmetic at -2000000000 asks for 32 GB
	// and takes the process with it. Nothing legitimate wants more of the end
	// than a forward slice would give, so it is the same cap.
	if want > maxSliceLines {
		want = maxSliceLines
	}
	br := bufio.NewReader(r)
	ring := make([]string, want)
	total := 0
	for {
		line, rerr := br.ReadString('\n')
		if line != "" || rerr == nil {
			ring[total%want] = strings.TrimSuffix(line, "\n")
			total++
		}
		if rerr != nil {
			break
		}
	}
	if total == 0 {
		return "", fmt.Errorf("%s is empty", rel)
	}
	first := total - want
	if first < 0 {
		first = 0
	}
	// Backwards, so a tail too big for the budget loses its oldest lines rather
	// than its newest — the end is the part that was asked for.
	spent := 0
	for i := total - 1; i >= first; i-- {
		spent += len(ring[i%want]) + 8
		if spent > maxReadBytes {
			first = i + 1
			break
		}
	}
	var b strings.Builder
	if first > 0 {
		fmt.Fprintf(&b, "[the last %d lines of %s; %d lines come before them.]\n",
			total-first, rel, first)
	}
	for i := first; i < total; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, ring[i%want])
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
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path) + tidy(abs), nil
}

// tidy formats a source file the agent just changed, and reports what it found.
//
// Two jobs, and the second is the valuable one.
//
// Formatting, because a small model writes badly-indented code and then spends
// turns fixing it. Watched on the ask-for-a-feature task: one run reached
// correct logic and then took eleven turns and twelve minutes, leaving bodies
// at three different depths and braces adrift; another produced a function with
// no indentation at all. Neither is a thinking problem, and neither is worth a
// model round trip when gofmt is deterministic and takes milliseconds.
//
// And a syntax check, free with it. gofmt has to parse, so a file that will not
// build says so here — one turn after the edit that broke it, rather than
// whenever the model next thinks to run a build. A parse failure leaves the
// file exactly as written: the model's text is its own, and silently reverting
// an edit it believes it made is a worse turn than an unformatted file.
func tidy(abs string) string {
	if filepath.Ext(abs) != ".go" {
		return ""
	}
	before, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	after, err := format.Source(before)
	if err != nil {
		// Reported as the compiler would put it, minus the absolute path.
		return fmt.Sprintf("\n[this file does not parse: %s. Fix it before moving on.]",
			strings.TrimPrefix(err.Error(), abs+":"))
	}
	if bytes.Equal(before, after) {
		return ""
	}
	if err := os.WriteFile(abs, after, 0o644); err != nil {
		return ""
	}
	return "\n[reformatted with gofmt]"
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
	// An entry with no path of its own continues with the file the one before
	// it named, falling back to the call's path.
	//
	// This is how the list actually gets written. Traced on rename-symbol: the
	// model named a file on the first entries and left it off a later one, which
	// is the natural shorthand for "still that file" — and rejecting it cost a
	// turn to be told something the batch already implied. The call's own path
	// still wins for entry one, so the single-file form is unchanged.
	running := defaultPath
	for i, e := range edits {
		p := e.Path
		if p == "" {
			p = running
		}
		if p == "" {
			return "", fmt.Errorf("edit %d has no path, and no earlier entry named one: give the call "+
				"a path, or a path on the first entry for each file", i+1)
		}
		running = p
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
	for i, p := range order {
		abs, err := w.resolve(p)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, []byte(staged[p]), 0o644); err != nil {
			return "", fmt.Errorf("%s: %w", p, err)
		}
		// Per file, and attached to that file's own line: a rename across ten
		// files that broke one of them should say which one.
		reports[i] += tidy(abs)
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
	return out.report + tidy(abs), nil
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
		shown, note := want, ""
		if shown > maxQuotedLines {
			shown, note = maxQuotedLines, fmt.Sprintf(" (the first %d lines of %d)", maxQuotedLines, want)
		}
		return fmt.Sprintf("%s. It starts at line %d, but the rest does not match. The file has%s:\n%s\n"+
			"Copy that block exactly, including leading whitespace, and change only what you mean to.",
			msg, n, note, quoteLines(regionAt(text, n, shown)))
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

// maxQuotedLines caps the region echoed back when an edit does not match.
//
// The quote used to be sized from the model's own old_str, so a model that
// passed a forty-line block got forty lines of the file back — and a run that
// tried to rewrite a README whole filled a phone screen and a half with red.
// The message exists to re-anchor the next attempt, and a dozen lines does
// that; past there the model should read the file rather than have the error
// read it aloud.
const maxQuotedLines = 12

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
	running := defaultPath
	for _, e := range edits {
		p := e.Path
		if p == "" {
			p = running // same rule as applyEditsAcross, or the two disagree
		}
		if p == "" {
			continue
		}
		running = p
		if seen[p] {
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

// Widening a read: the neighbours' shape, not their contents.
//
// Two traced sessions and two screenshots say the same thing. Asked to look at
// the TUI and the mobile app, a model opened one file per turn for six minutes
// — main.dart, then chat_screen.dart, then tui.go, then theme.go — each one a
// round trip, each preceded by a sentence announcing it. The tool description
// says read takes twenty paths. The system prompt says to ask for everything at
// once. A hint fires at the moment it is actionable and says it again. It read
// one file per call anyway.
//
// The first attempt sent whole sibling files along with the requested one, and
// it was wrong twice over. Measured against this repository it never fired —
// an 8 KB per-file ceiling excluded every real neighbour, since Go and Dart
// files here run 13 to 31 KB — and sizing the caps to fit them would have put
// ~25k tokens of unrequested content into a context that was already full.
//
// So this sends what a neighbour is, not what it says: the top-level
// declarations, which on ten real files came to 4.3% of the bytes — 90 KB of
// source, 3.8 KB of outline. That is the part a model actually needs from a
// package it has not opened. Knowing theme.go defines Palette and lipgloss
// styles is what stops it guessing; the body can be read if and when it matters.
//
// The heuristic is deliberately language-agnostic: a line that starts in column
// zero, is not blank, and is not a comment or a closing bracket. That is a
// top-level declaration in Go, Dart, Python, JavaScript and most things shaped
// like them, and a language it reads badly costs a few useless lines rather
// than a wrong answer.
const (
	neighbourFiles   = 8    // outlines at most
	neighbourDecls   = 25   // declarations per file
	neighbourBudget  = 6000 // characters of outline in total
	neighbourDirMax  = 60   // skip directories bigger than this: not a component
	neighbourLineMax = 120  // one declaration is a signature, not a paragraph
)

// declarations returns the top-level lines of a source file.
func declarations(body string) []string {
	var out []string
	for _, ln := range strings.Split(body, "\n") {
		if ln == "" || ln[0] == ' ' || ln[0] == '\t' {
			continue
		}
		t := strings.TrimRight(ln, " \t")
		switch {
		case t == "":
		case strings.HasPrefix(t, "//"), strings.HasPrefix(t, "/*"),
			strings.HasPrefix(t, "*"), strings.HasPrefix(t, "#"):
		case t == "}", t == ")", t == "};", t == "]", strings.HasPrefix(t, "}"):
		default:
			if len(t) > neighbourLineMax {
				t = t[:neighbourLineMax] + "…"
			}
			out = append(out, t)
			if len(out) >= neighbourDecls {
				return out
			}
		}
	}
	return out
}

// neighbourhood outlines the other files in a requested file's directory.
//
// Returns "" when there is nothing worth saying: a multi-file request (that
// model is already batching), a directory too big to be a component, or no
// unseen siblings of the same type.
func (w *workspace) neighbourhood(paths []string, seen map[string]bool) string {
	if len(paths) != 1 {
		return ""
	}
	abs, err := w.resolve(paths[0])
	if err != nil {
		return ""
	}
	dir := filepath.Dir(abs)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > neighbourDirMax {
		return ""
	}
	ext := filepath.Ext(abs)
	if ext == "" {
		return ""
	}
	var b strings.Builder
	spent, shown := 0, 0
	for _, e := range entries {
		if shown >= neighbourFiles || spent >= neighbourBudget {
			break
		}
		if e.IsDir() || filepath.Ext(e.Name()) != ext {
			continue
		}
		sib := filepath.Join(dir, e.Name())
		if sib == abs {
			continue
		}
		label := w.label(sib)
		if seen[label] || seen[sib] {
			continue
		}
		body, err := os.ReadFile(sib)
		if err != nil {
			continue
		}
		decls := declarations(string(body))
		if len(decls) == 0 {
			continue
		}
		entry := "\n" + label + ":\n  " + strings.Join(decls, "\n  ") + "\n"
		if spent+len(entry) > neighbourBudget {
			continue
		}
		b.WriteString(entry)
		spent += len(entry)
		shown++
	}
	if shown == 0 {
		return ""
	}
	return fmt.Sprintf("\n\n[The rest of %s, top-level declarations only — you did not ask for "+
		"these and they are not the files' contents. Read one for its body when you need it, "+
		"rather than opening them one at a time to find out what they are.]\n%s",
		w.label(dir), b.String())
}
