package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func envCmd() (*cobra.Command, *string, *int, *bool) {
	var s string
	var n int
	var b bool
	c := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	c.Flags().StringVar(&s, "auth-client-id", "", "")
	c.Flags().IntVar(&n, "max-turns", 0, "")
	c.Flags().BoolVar(&b, "auth-insecure-cookies", false, "")
	return c, &s, &n, &b
}

// Every flag reads its own variable, derived from the name: dashes become
// underscores and the whole thing is upper-cased under a DROSS_ prefix.
func TestEnvFillsAnyFlag(t *testing.T) {
	c, s, n, b := envCmd()
	t.Setenv("DROSS_AUTH_CLIENT_ID", "abc123")
	t.Setenv("DROSS_MAX_TURNS", "42")
	t.Setenv("DROSS_AUTH_INSECURE_COOKIES", "true")

	if err := bindEnv(c); err != nil {
		t.Fatalf("bindEnv: %v", err)
	}
	if *s != "abc123" || *n != 42 || !*b {
		t.Errorf("got %q / %d / %v", *s, *n, *b)
	}
}

// An argument is a deliberate override of the environment it runs in.
func TestCommandLineBeatsTheEnvironment(t *testing.T) {
	c, s, _, _ := envCmd()
	t.Setenv("DROSS_AUTH_CLIENT_ID", "from-env")
	if err := c.Flags().Parse([]string{"--auth-client-id", "from-flag"}); err != nil {
		t.Fatal(err)
	}
	if err := bindEnv(c); err != nil {
		t.Fatal(err)
	}
	if *s != "from-flag" {
		t.Errorf("env overrode an explicit argument: %q", *s)
	}
}

// A value from the environment counts as specified, or the layers below treat
// it as absent and the config file it should override wins instead.
func TestEnvValueCountsAsSpecified(t *testing.T) {
	c, _, _, _ := envCmd()
	t.Setenv("DROSS_AUTH_CLIENT_ID", "abc")
	if err := bindEnv(c); err != nil {
		t.Fatal(err)
	}
	if !c.Flags().Changed("auth-client-id") {
		t.Error("flag set from the environment is not marked as changed")
	}
	if got := envSource(c, "auth-client-id"); got != "$DROSS_AUTH_CLIENT_ID" {
		t.Errorf("envSource = %q", got)
	}
	if got := envSource(c, "max-turns"); got != "" {
		t.Errorf("unset flag claims an env source: %q", got)
	}
}

// A malformed value has to be reported. Silently keeping the default would let
// a typo in a shell profile change how the agent runs with no sign of it.
func TestUnparseableEnvValueIsAnError(t *testing.T) {
	c, _, _, _ := envCmd()
	t.Setenv("DROSS_MAX_TURNS", "not-a-number")
	err := bindEnv(c)
	if err == nil {
		t.Fatal("accepted a non-numeric value for an int flag")
	}
	if got := err.Error(); !strings.Contains(got, "DROSS_MAX_TURNS") {
		t.Errorf("error does not name the variable: %s", got)
	}
}

// An empty variable is the same as not setting one; exporting DROSS_FOO="" to
// clear it must not be read as an explicit empty value.
func TestEmptyEnvIsIgnored(t *testing.T) {
	c, s, _, _ := envCmd()
	t.Setenv("DROSS_AUTH_CLIENT_ID", "")
	if err := bindEnv(c); err != nil {
		t.Fatal(err)
	}
	if *s != "" || c.Flags().Changed("auth-client-id") {
		t.Error("an empty variable was treated as a value")
	}
}
