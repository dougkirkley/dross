package agent

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// What a turn actually consulted.
//
// The point of collecting these is that a citation has to be true, and the only
// way to guarantee that is to take it from the tool that fetched it rather than
// from the model that read it. A local model asked to list its sources will
// produce plausible URLs it has never seen — worse than no citations, because a
// fabricated one looks checkable and costs somebody a click to find out.
//
// So sources are recorded where the fetching happens, and the model's prose has
// no say in what appears in the list.

// Source is one thing a turn looked at.
type Source struct {
	Title   string `json:"title,omitempty"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
	// Site is the bare host, for a UI that wants to show where something came
	// from without printing the whole URL.
	Site string `json:"site,omitempty"`
}

// NewSource builds a Source, filling in the host and refusing anything that is
// not a real absolute web URL — a relative link or a javascript: scheme in a
// citation list is either a bug or an attack.
func NewSource(title, rawURL, snippet string) (Source, bool) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Source{}, false
	}
	return Source{
		Title:   strings.TrimSpace(title),
		URL:     rawURL,
		Snippet: strings.TrimSpace(snippet),
		Site:    strings.TrimPrefix(u.Host, "www."),
	}, true
}

// maxSources is how many citations are worth showing.
//
// Four searches at eight results each is thirty-two links under an answer,
// which is not a citation list - it is a second search results page, and it
// buries the two or three that were actually used. Searching widely is cheap
// and worth it; showing everything found is not the same thing as showing the
// work.
const maxSources = 10

// dedupeSources keeps the first mention of each URL, in order, and stops at
// maxSources. Order is the order the turn met them, so the main query's results
// lead and the site-specific ones follow - which is also roughly the order of
// usefulness.
func dedupeSources(in []Source) []Source {
	seen := map[string]bool{}
	out := make([]Source, 0, len(in))
	for _, s := range in {
		if s.URL == "" || seen[s.URL] {
			continue
		}
		seen[s.URL] = true
		out = append(out, s)
		if len(out) == maxSources {
			break
		}
	}
	return out
}

// encodeSources renders sources for an event payload.
func encodeSources(in []Source) string {
	b, err := json.Marshal(dedupeSources(in))
	if err != nil {
		return ""
	}
	return string(b)
}

// Collecting happens through the context, because a tool is a plain function
// called from somewhere deep in a turn and threading a collector through every
// signature to serve one feature would be a poor trade.

type sourceKey struct{}

type sourceSink struct {
	mu   sync.Mutex
	list []Source
}

// withSourceSink returns a context that collects sources for one turn.
func withSourceSink(ctx context.Context) (context.Context, *sourceSink) {
	s := &sourceSink{}
	return context.WithValue(ctx, sourceKey{}, s), s
}

// recordSource notes something a tool consulted. A no-op when nothing is
// collecting, so tools stay callable outside a turn.
func recordSource(ctx context.Context, src Source) {
	s, _ := ctx.Value(sourceKey{}).(*sourceSink)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, src)
}

func (s *sourceSink) all() []Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	return dedupeSources(s.list)
}

// pageTitle pulls <title> out of HTML so a citation reads as the page's name
// rather than its URL. Best effort: a page without one cites by address, which
// is honest if less pretty.
func pageTitle(html string) string {
	lower := strings.ToLower(html)
	i := strings.Index(lower, "<title")
	if i < 0 {
		return ""
	}
	open := strings.Index(lower[i:], ">")
	if open < 0 {
		return ""
	}
	start := i + open + 1
	end := strings.Index(lower[start:], "</title>")
	if end < 0 {
		return ""
	}
	title := strings.TrimSpace(html[start : start+end])
	if len(title) > 200 {
		title = title[:200]
	}
	return title
}

// isRetrievalTool reports whether a tool's output is worth reading citations
// out of. Named conservatively: a tool that writes files and happens to print a
// URL is not a source, and treating it as one puts a link in front of somebody
// that nothing actually fetched.
func isRetrievalTool(name string) bool {
	n := strings.ToLower(name)
	if !strings.HasPrefix(n, "web_") {
		return false
	}
	return strings.Contains(n, "search") || strings.Contains(n, "fetch") || strings.Contains(n, "crawl")
}

// exaEntry matches the "Title: …\nURL: …" pairs Exa returns over MCP.
var exaEntry = regexp.MustCompile(`(?m)^Title:[ \t]*(.+)\r?\n(?:.*\r?\n)*?URL:[ \t]*(\S+)`)

// bareURL is the fallback for a tool that formats its results some other way.
var bareURL = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

// recordSourcesFromToolOutput reads citations out of a tool's text output.
//
// Our own tools record as they go, which is exact. A tool from somewhere else
// can only be read after the fact, so this prefers the structured form and
// falls back to finding URLs — accepting that a fallback citation is "a link
// this tool returned" rather than "a page that was definitely read". Both are
// true statements about where the answer came from, which is the bar; what
// neither can be is a URL the model invented, because the model never touched
// this string.
func recordSourcesFromToolOutput(ctx context.Context, tool, output string) {
	if !isRetrievalTool(tool) || output == "" {
		return
	}
	if m := exaEntry.FindAllStringSubmatch(output, -1); len(m) > 0 {
		for _, g := range m {
			if src, ok := NewSource(strings.TrimSpace(g[1]), strings.TrimSpace(g[2]), ""); ok {
				recordSource(ctx, src)
			}
		}
		return
	}
	for _, u := range bareURL.FindAllString(output, 20) {
		u = strings.TrimRight(u, ".,;:")
		if src, ok := NewSource("", u, ""); ok {
			recordSource(ctx, src)
		}
	}
}

// SearchWeb runs a search outside of a turn, for a caller that wants results
// before the model sees the prompt rather than if the model asks for them.
//
// It returns the formatted text a model reads and the sources behind it, so the
// caller can put one in the context and the other in front of the reader.
func SearchWeb(ctx context.Context, query string) (string, []Source, error) {
	ctx, sink := withSourceSink(ctx)
	out, err := webSearch(ctx, query)
	if err != nil {
		return "", nil, err
	}
	return out, sink.all(), nil
}

// WithSeedSources gives a turn sources that were gathered before it started, so
// what the reader sees is everything the answer was built from and not only the
// part the model went looking for.
func WithSeedSources(src []Source) Option {
	return func(a *Agent) { a.seedSources = append(a.seedSources, src...) }
}

// SeedSources is the same thing after construction, for retrieval that runs in
// the background so the request that started it can return.
func (a *Agent) SeedSources(src []Source) { a.seedSources = append(a.seedSources, src...) }

// citationMarkup matches the bracketed reference tags some models emit around
// a source number, e.g. "[REF]1[/REF]" from devstral.
var citationMarkup = regexp.MustCompile(`\[REF\]\s*\d+\s*\[/REF\]`)

// stripCitationMarkup removes those tags from prose.
//
// They are a model's private notation for "this came from source 1", and no
// client renders them — devstral's turned up verbatim in the middle of
// sentences, which reads as the answer having glitched. The sources rail
// already shows what the turn fetched, in order, so the information the tag
// carries is on screen either way. Removing the tag is the smallest fix that
// does not silently drop the source.
//
// Deliberately narrow: only this exact shape, so ordinary square brackets in
// prose or code are untouched.
func stripCitationMarkup(text string) string {
	if !strings.Contains(text, "[REF]") {
		return text
	}
	out := citationMarkup.ReplaceAllString(text, "")
	// A tag sitting before punctuation leaves " ." behind.
	out = strings.ReplaceAll(out, " .", ".")
	out = strings.ReplaceAll(out, " ,", ",")
	return strings.TrimSpace(out)
}

// markdownLink matches [text](url) so a link can be checked against what was
// actually fetched.
var markdownLink = regexp.MustCompile(`\[([^\]]*)\]\((https?://[^)\s]+)\)`)

// keepOnlyRetrievedLinks removes links to pages this turn did not retrieve.
//
// Inline citations are the most useful form and the most dangerous: a link is a
// claim that a specific page says a specific thing, and it invites a tap to
// check. A model writing from search results will sometimes produce a URL that
// looks like one it saw and is not, and the reader has no way to tell the
// difference until they follow it.
//
// So the set of linkable URLs is the set the turn fetched, and nothing else
// survives as a link. The link text is kept — the sentence is still the model's
// point, and deleting words to enforce a citation rule would be a stranger
// intervention than removing the citation.
func keepOnlyRetrievedLinks(ctx context.Context, text string) string {
	sink, _ := ctx.Value(sourceKey{}).(*sourceSink)
	if sink == nil || !strings.Contains(text, "](") {
		return text
	}
	allowed := map[string]bool{}
	for _, s := range sink.all() {
		allowed[s.URL] = true
		allowed[strings.TrimRight(s.URL, "/")] = true
	}
	if len(allowed) == 0 {
		return text
	}
	return markdownLink.ReplaceAllStringFunc(text, func(m string) string {
		g := markdownLink.FindStringSubmatch(m)
		if len(g) != 3 {
			return m
		}
		url := strings.TrimRight(g[2], "/.,;:")
		if allowed[g[2]] || allowed[url] {
			return m
		}
		return g[1] // the words, without the promise
	})
}
