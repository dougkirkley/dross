package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
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
