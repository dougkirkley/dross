package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
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

// Every backend has to record, because which one answers depends on whether a
// key is set - and the day the Exa key started working, the only backend that
// recorded was the one no longer being used. Search kept working, sources went
// silently to zero, and the guard downstream read that as "nothing found" and
// switched retrieval off.
func TestEverySearchBackendRecordsSources(t *testing.T) {
	src, err := os.ReadFile("tools.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	for _, fn := range []string{"func exaSearch", "func braveSearch", "func ddgSearch"} {
		i := strings.Index(body, fn)
		if i < 0 {
			t.Fatalf("%s is gone; this test needs updating", fn)
		}
		// Each backend's body runs until the next top-level func.
		rest := body[i+len(fn):]
		if j := strings.Index(rest, "\nfunc "); j >= 0 {
			rest = rest[:j]
		}
		if !strings.Contains(rest, "recordSource(") {
			t.Errorf("%s does not record sources: a search through it would cite nothing", fn)
		}
	}
}

// An inline link is a claim about a specific page and invites a tap to check
// it. So only pages this turn actually retrieved survive as links; the rest
// keep their words and lose the promise.
func TestOnlyRetrievedPagesStayLinked(t *testing.T) {
	ctx, _ := withSourceSink(context.Background())
	for _, u := range []string{"https://reccs.co/home/mattresses", "https://www.reddit.com/r/Mattress/x"} {
		if src, ok := NewSource("", u, ""); ok {
			recordSource(ctx, src)
		}
	}

	in := "The [Reddit thread](https://www.reddit.com/r/Mattress/x) likes it, " +
		"and [reccs](https://reccs.co/home/mattresses/) agrees, " +
		"but [this review](https://invented.example/review) is made up."
	got := keepOnlyRetrievedLinks(ctx, in)

	if !strings.Contains(got, "[Reddit thread](https://www.reddit.com/r/Mattress/x)") {
		t.Error("a retrieved page lost its link")
	}
	// A trailing slash is the same page, not a different one.
	if !strings.Contains(got, "[reccs](https://reccs.co/home/mattresses/)") {
		t.Error("a trailing slash cost a real citation")
	}
	if strings.Contains(got, "invented.example") {
		t.Error("a link to a page that was never fetched survived")
	}
	// The words stay: the sentence is still the model's point.
	if !strings.Contains(got, "this review is made up") {
		t.Errorf("the sentence was damaged rather than unlinked: %q", got)
	}
}

// With nothing retrieved there is nothing to check against, so the text is left
// alone rather than stripped of every link on no evidence.
func TestLinksSurviveWhenNothingWasRetrieved(t *testing.T) {
	ctx, _ := withSourceSink(context.Background())
	in := "see [a page](https://example.com/x)"
	if got := keepOnlyRetrievedLinks(ctx, in); got != in {
		t.Errorf("stripped links with no source set to judge against: %q", got)
	}
}
