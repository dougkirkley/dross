package tui

import "testing"

func TestLooksLikeMash(t *testing.T) {
	mash := []string{
		"asdfghjkl",
		"aaaaaaaaaa",
		"sdfghjk",
		"qwertyui",
		"jkljkljkl",
		"zxcvbnm",
		"hjkkkkkk",
		"fdsafdsa",
	}
	for _, s := range mash {
		if !looksLikeMash(s) {
			t.Errorf("expected mash: %q", s)
		}
	}

	// Real prompts must never be blocked — a false positive stops real work.
	real := []string{
		"hi",
		"go",
		"fix the auth guard",
		"run the tests and fix any failures",
		"grep for TODO in internal/tui",
		"why does the build fail?",
		"refactor internal/cli/tui.go",
		"add a --json flag",
		"bump to 0.5.31 and deploy",
		"rhythm",
		"deploy",
		"status",
		"explain this stack trace",
		"what changed in the last commit",
		"schema",
		"strength",
	}
	for _, s := range real {
		if looksLikeMash(s) {
			t.Errorf("expected real input, got flagged: %q", s)
		}
	}
}
