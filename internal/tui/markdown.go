package tui

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// renderMarkdown renders assistant markdown to styled terminal lines wrapped to
// width, using glamour with a zero-margin dark style so output aligns flush with
// the rest of the transcript instead of glamour's default indented block.
func renderMarkdown(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	s = tagCodeFences(s)
	r := mdRenderer(width)
	if r == nil {
		return strings.Split(s, "\n")
	}
	out, err := r.Render(s)
	if err != nil {
		return strings.Split(s, "\n")
	}
	out = strings.Trim(out, "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// mdRenderer builds a glamour renderer for the given width. Renderers are cheap
// to build but width-dependent, so we cache the last one.
var (
	mdCachedWidth int
	mdCached      *glamour.TermRenderer
)

func mdRenderer(width int) *glamour.TermRenderer {
	if mdCached != nil && mdCachedWidth == width {
		return mdCached
	}
	cfg := styles.DarkStyleConfig
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""
	// Recolor the accent-bearing elements from the live palette so markdown
	// follows the active theme instead of glamour's fixed dark colors.
	acc := string(accent)
	accHi := string(accentHi)
	body := string(prose)
	cfg.Code.Color = &accHi // inline code (was glamour's red/salmon)
	cfg.Code.BackgroundColor = nil
	cfg.Link.Color = &accHi
	cfg.LinkText.Color = &acc
	cfg.Text.Color = &body
	cfg.Paragraph.Color = &body
	for _, h := range []*ansi.StyleBlock{
		&cfg.Heading, &cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6,
	} {
		h.Color = &acc
		h.BackgroundColor = nil
		// Drop glamour's literal "## " prefix so headings render as clean styled
		// subtitles (bold accent), not with the raw markdown markers.
		h.Prefix = ""
		h.BlockPrefix = ""
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	mdCached, mdCachedWidth = r, width
	return r
}

// extLang maps a file extension (or bare filename, lowercased, no leading dot)
// to the chroma lexer name glamour needs for syntax highlighting.
var extLang = map[string]string{
	"go": "go", "py": "python", "ts": "typescript", "tsx": "tsx",
	"js": "javascript", "jsx": "jsx", "mjs": "javascript", "cjs": "javascript",
	"rs": "rust", "rb": "ruby", "java": "java", "kt": "kotlin", "kts": "kotlin",
	"c": "c", "h": "c", "cpp": "cpp", "cc": "cpp", "cxx": "cpp", "hpp": "cpp",
	"cs": "csharp", "php": "php", "swift": "swift", "m": "objective-c",
	"sh": "bash", "bash": "bash", "zsh": "bash", "fish": "fish", "ps1": "powershell",
	"yaml": "yaml", "yml": "yaml", "json": "json", "toml": "toml", "ini": "ini",
	"xml": "xml", "md": "markdown", "sql": "sql", "html": "html", "htm": "html",
	"css": "css", "scss": "scss", "sass": "sass", "less": "less", "dart": "dart",
	"proto": "protobuf", "lua": "lua", "r": "r", "scala": "scala", "clj": "clojure",
	"ex": "elixir", "exs": "elixir", "erl": "erlang", "hs": "haskell", "ml": "ocaml",
	"vim": "vim", "pl": "perl", "tf": "hcl", "hcl": "hcl", "gradle": "groovy",
	"groovy": "groovy", "makefile": "make", "mk": "make", "dockerfile": "dockerfile",
}

// fileRE matches a filename/path token bearing an extension, e.g. cmd/dross/main.go.
var fileRE = regexp.MustCompile(`[\w./-]+\.[A-Za-z0-9]+`)

// langFromName resolves a chroma language from a filename or path, matching a
// bare basename (Dockerfile, Makefile) before falling back to the extension.
func langFromName(name string) string {
	base := strings.ToLower(filepath.Base(name))
	if l, ok := extLang[base]; ok {
		return l
	}
	ext := strings.TrimPrefix(filepath.Ext(base), ".")
	return extLang[ext]
}

// tagCodeFences adds a language hint to fenced code blocks that lack one, so
// glamour can syntax-highlight them. When the opening fence carries a filename
// (```main.go) it maps the extension; otherwise it infers from a file path on
// the nearest preceding non-blank line (the common "see foo.go:" then a block
// pattern). Fences that already name a language are left untouched.
func tagCodeFences(s string) string {
	lines := strings.Split(s, "\n")
	inCode := false
	for i, l := range lines {
		t := strings.TrimLeft(l, " \t")
		if !strings.HasPrefix(t, "```") {
			continue
		}
		if inCode {
			inCode = false
			continue
		}
		inCode = true
		indent := l[:len(l)-len(t)]
		info := strings.TrimSpace(strings.TrimPrefix(t, "```"))
		if info == "" {
			if lang := inferFenceLang(lines, i); lang != "" {
				lines[i] = indent + "```" + lang
			}
			continue
		}
		// A non-empty info word may already be a language (leave it) or a
		// filename/path (map it): rewrite only when it resolves to a different tag.
		if lang := langFromName(info); lang != "" && lang != info {
			lines[i] = indent + "```" + lang
		}
	}
	return strings.Join(lines, "\n")
}

// inferFenceLang looks at the nearest non-blank line before an untagged fence
// for a file path and returns its language, or "" if none is found.
func inferFenceLang(lines []string, i int) string {
	for j := i - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		for _, name := range fileRE.FindAllString(lines[j], -1) {
			if lang := langFromName(name); lang != "" {
				return lang
			}
		}
		return "" // only consult the single nearest non-blank line
	}
	return ""
}
