package agent

import (
	"strings"
	"testing"
)

// Every tool's description must name the tool it belongs to.
//
// The models were not being careless: grep was described as "Search file
// contents" and web_search as "Search the web", so two tools opened with the
// same verb and neither said its own name. A model reading that calls
// "search", loses the turn, and is told off for a wording dross chose.
func TestToolDescriptionsNameTheirTool(t *testing.T) {
	for _, d := range toolDefs(true, true, true) {
		first := strings.SplitN(d.Description, " ", 2)[0]
		if !strings.HasPrefix(strings.ToLower(first), strings.ToLower(d.Name)) {
			t.Errorf("%s: description opens with %q, not its own name:\n  %s",
				d.Name, first, d.Description)
		}
	}
}

// The two searches must each point at the other, since which one is meant is
// the only genuinely ambiguous choice in the set.
func TestSearchToolsDisambiguateEachOther(t *testing.T) {
	got := map[string]string{}
	for _, d := range toolDefs(true, true, true) {
		got[d.Name] = d.Description
	}
	if !strings.Contains(got["grep"], "web_search") {
		t.Error("grep does not mention web_search")
	}
	if !strings.Contains(got["web_search"], "grep") {
		t.Error("web_search does not mention grep")
	}
}
