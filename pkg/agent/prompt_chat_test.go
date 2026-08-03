package agent

import (
	"strings"
	"testing"
)

// The prompt has to describe the tools the session was actually given. It once
// said "you have one tool: web_fetch" while the session also held web_search,
// so the model never knew it could search and answered from memory - a prompt
// that lists the wrong tools rules out the right move.
func TestChatPromptDescribesTheToolsItWasGiven(t *testing.T) {
	withSearch := chatPrompt([]string{"web_search", "web_fetch"})
	if !strings.Contains(withSearch, "web_search(query)") {
		t.Error("a session with search was not told it can search")
	}
	if !strings.Contains(withSearch, "web_fetch(url)") {
		t.Error("a session with fetch was not told it can fetch")
	}

	// Exa is the better backend, so it leads when present.
	withExa := chatPrompt([]string{"web_search", "web_fetch", "web_search_exa", "web_fetch_exa"})
	if !strings.Contains(withExa, "web_search_exa(query)") {
		t.Error("exa search was not offered")
	}
	if strings.Index(withExa, "web_search_exa") > strings.Index(withExa, "prefer it")+len(withExa)/2 {
		t.Error("the preferred backend should be listed first")
	}

	// And a session with neither must not be told it can browse.
	bare := chatPrompt(nil)
	if strings.Contains(bare, "web_search(query)") || strings.Contains(bare, "web_fetch(url)") {
		t.Error("a session with no retrieval tools was told it has them")
	}
}

// The instructions that make citations possible at all.
func TestChatPromptRequiresLookingThingsUp(t *testing.T) {
	p := chatPrompt([]string{"web_search", "web_fetch"})
	for _, want := range []string{
		"Search before answering",                        // retrieval is the default, not a fallback
		"training data is stale",                         // why memory is not good enough
		"did not look something up",                      // say so rather than implying otherwise
		"Never write a URL you have not fetched",         // the anti-fabrication rule
		"\"reddit\" and \"youtube review\" in the query", // where the honest reviews are
		"rather than using a site: filter",               // the operator is ignored by these backends
	} {
		if !strings.Contains(p, want) {
			t.Errorf("chat prompt is missing %q", want)
		}
	}
}

// The advanced search is only offered when the MCP server was asked to expose
// it — describing a tool the session does not hold is how the last version of
// this prompt sent the model looking for something that was not there.
func TestChatPromptOffersAdvancedSearchOnlyWhenPresent(t *testing.T) {
	with := chatPrompt([]string{"web_search_exa", "web_fetch_exa", "web_search_advanced_exa"})
	if !strings.Contains(with, "web_search_advanced_exa(query)") {
		t.Error("advanced search was enabled but not described")
	}
	without := chatPrompt([]string{"web_search_exa", "web_fetch_exa"})
	if strings.Contains(without, "web_search_advanced_exa") {
		t.Error("described a tool this session does not have")
	}
}
