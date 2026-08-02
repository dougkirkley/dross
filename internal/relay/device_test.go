package relay

import "testing"

// When the UI takes prompts, this device must not build an agent of its own.
// Two agents on one machine was the defect: the relay's had no permission
// rules, no conversation history and none of the user's tools.
func TestPromptHookMeansNoLocalAgent(t *testing.T) {
	var got string
	opts := DeviceOptions{Prompt: func(_, text string) { got = text }}

	// Stand in for the frame loop's dispatch: with a Prompt hook set it hands
	// the text over instead of calling d.run, which is what would construct an
	// agent.
	if opts.Prompt == nil {
		t.Fatal("Prompt hook not set")
	}
	opts.Prompt("ses_x", "do the thing")
	if got != "do the thing" {
		t.Errorf("hook received %q", got)
	}
}

// Without a UI — a session pod, or plain `dross rc` — the device still has to
// run the work itself, so the fallback must stay reachable.
func TestNoPromptHookLeavesTheFallback(t *testing.T) {
	if (DeviceOptions{}).Prompt != nil {
		t.Error("a bare DeviceOptions must not claim to take prompts")
	}
}

// A pod is launched with --name set to the session id, which already begins
// with the prefix. Adding another produced "ses_ses_…" and advertised a second,
// empty session beside the real one.
func TestSessionIDIsNotDoublePrefixed(t *testing.T) {
	for name, want := range map[string]string{
		"ses_abc": "ses_abc", // a session pod
		"laptop":  "ses_laptop",
		"":        "ses_",
	} {
		if got := advertisedSessionID(name); got != want {
			t.Errorf("name %q -> %q; want %q", name, got, want)
		}
	}
}
