package tui

import (
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
