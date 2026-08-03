package cli

import "testing"

// A credential that half-expanded is worse than one that is absent: "Bearer "
// is refused where no header at all is often allowed, so an unset key would
// take access away rather than fail to add it.
func TestExpandAllReportsMissingVariables(t *testing.T) {
	t.Setenv("PRESENT", "value")
	if got, ok := expandAll("Bearer $PRESENT"); !ok || got != "Bearer value" {
		t.Errorf("present: %q ok=%v", got, ok)
	}
	if _, ok := expandAll("Bearer $DEFINITELY_NOT_SET_HERE"); ok {
		t.Error("an unset variable was reported as a complete expansion")
	}
	if got, ok := expandAll("no variables"); !ok || got != "no variables" {
		t.Errorf("literal: %q ok=%v", got, ok)
	}
}
