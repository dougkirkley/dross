package agent

import (
	"context"
	"encoding/json"
	"net/url"
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

// dedupeSources keeps the first mention of each URL, in order. A turn that
// searches twice and fetches one of the results should cite that page once.
func dedupeSources(in []Source) []Source {
	seen := map[string]bool{}
	out := make([]Source, 0, len(in))
	for _, s := range in {
		if s.URL == "" || seen[s.URL] {
			continue
		}
		seen[s.URL] = true
		out = append(out, s)
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
