package agent

import (
	"context"
	"encoding/json"
	"testing"
)

// A citation has to be true, so it comes from the tool that fetched it. These
// are the rules that keep the list honest.
func TestSourcesAreOnlyRealFetchedURLs(t *testing.T) {
	ctx, sink := withSourceSink(context.Background())

	// What a search returned, and a page that was actually read.
	for _, c := range []struct{ title, url, snippet string }{
		{"Best mattresses 2026", "https://example.com/mattresses", "our picks"},
		{"Duplicate", "https://example.com/mattresses", "seen again"},
		{"Read page", "https://other.example/guide", ""},
	} {
		if src, ok := NewSource(c.title, c.url, c.snippet); ok {
			recordSource(ctx, src)
		}
	}
	// Things that must never become citations.
	for _, bad := range []string{"", "/relative/path", "javascript:alert(1)", "not a url", "ftp://files.example"} {
		if _, ok := NewSource("bad", bad, ""); ok {
			t.Errorf("%q was accepted as a source", bad)
		}
	}

	got := sink.all()
	if len(got) != 2 {
		t.Fatalf("got %d sources; want 2 after dedupe: %+v", len(got), got)
	}
	if got[0].URL != "https://example.com/mattresses" || got[0].Site != "example.com" {
		t.Errorf("first source = %+v", got[0])
	}
	// The first mention wins, so a page cited then re-listed keeps its title.
	if got[0].Title != "Best mattresses 2026" {
		t.Errorf("dedupe kept the wrong title: %q", got[0].Title)
	}

	var round []Source
	if err := json.Unmarshal([]byte(encodeSources(got)), &round); err != nil {
		t.Fatalf("sources do not survive the wire: %v", err)
	}
	if len(round) != 2 {
		t.Errorf("encoded %d sources; want 2", len(round))
	}
}

// Recording outside a turn is a no-op rather than a panic: tools are ordinary
// functions and are called from places that are not runs.
func TestRecordingWithoutASinkIsHarmless(t *testing.T) {
	src, ok := NewSource("t", "https://example.com", "")
	if !ok {
		t.Fatal("valid URL rejected")
	}
	recordSource(context.Background(), src) // must not panic
}

func TestPageTitle(t *testing.T) {
	if got := pageTitle(`<html><head><TITLE> Spaced Out </TITLE></head>`); got != "Spaced Out" {
		t.Errorf("title = %q", got)
	}
	if got := pageTitle(`<html><body>no title</body></html>`); got != "" {
		t.Errorf("missing title should be empty, got %q", got)
	}
}

// Exa answers over MCP with text, so its citations have to be read back out of
// it — our own tools record as they go, but a tool from somewhere else cannot.
// Without this a turn that searched through MCP would answer with sources and
// show none, which looks exactly like one that never searched.
func TestSourcesAreReadFromOtherPeoplesTools(t *testing.T) {
	ctx, sink := withSourceSink(context.Background())

	exaOutput := "Title: Best Mattresses 2026 - Reddit's Top Picks\n" +
		"URL: https://reccs.co/home/mattresses\n" +
		"Published: 2026-03-26T11:17:22.000Z\n" +
		"Highlights:\nsome text\n\n" +
		"Title: r/Mattress on hybrids\nURL: https://www.reddit.com/r/Mattress/comments/abc\n"
	recordSourcesFromToolOutput(ctx, "web_search_exa", exaOutput)

	got := sink.all()
	if len(got) != 2 {
		t.Fatalf("read %d sources from Exa output; want 2: %+v", len(got), got)
	}
	if got[0].Title != "Best Mattresses 2026 - Reddit's Top Picks" || got[0].Site != "reccs.co" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Site != "reddit.com" {
		t.Errorf("second lost its site: %+v", got[1])
	}

	// A tool that formats differently still yields its links.
	ctx2, sink2 := withSourceSink(context.Background())
	recordSourcesFromToolOutput(ctx2, "web_fetch_exa", "see https://example.com/page, and https://other.example/x.")
	if n := len(sink2.all()); n != 2 {
		t.Errorf("fallback extraction found %d; want 2", n)
	}

	// And a tool that is not retrieval contributes nothing, however many URLs
	// it happens to print.
	ctx3, sink3 := withSourceSink(context.Background())
	recordSourcesFromToolOutput(ctx3, "bash", "curl https://example.com && echo https://other.example")
	if n := len(sink3.all()); n != 0 {
		t.Errorf("a non-retrieval tool contributed %d sources; want 0", n)
	}
}
