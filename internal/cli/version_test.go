package cli

import (
	"strings"
	"testing"
)

// The point of the change: a version has to name a build, not only a release.
// A pod answering "v0.4.0" is consistent with every commit since somebody last
// bumped the constant, which is what made "is the deployment current?"
// unanswerable without diffing behaviour.
func TestVersionNamesTheBuild(t *testing.T) {
	got := FullVersion()
	if !strings.HasPrefix(got, Version) {
		t.Fatalf("%q does not start with the release string %q", got, Version)
	}
	// `go test` builds from the checkout, so the toolchain has a revision to
	// stamp and it must appear.
	if rev, _ := buildRevision(); rev != "" && !strings.Contains(got, rev) {
		t.Errorf("%q omits the revision %q the toolchain recorded", got, rev)
	}
}

// The image's context excludes .git, so that build has nothing to read and
// passes the commit in with -ldflags instead.
func TestTheImageCanStampItsOwnRevision(t *testing.T) {
	prev := revision
	t.Cleanup(func() { revision = prev })

	revision = "0123456789abcdef0123456789abcdef01234567"
	rev, _ := buildRevision()
	if rev == "" {
		t.Fatal("a build with no VCS information should fall back to the stamped revision")
	}
	// Whichever source won, it is abbreviated the way git prints it, so a
	// version line can be pasted into git log unedited.
	if len(rev) != 12 {
		t.Errorf("revision %q is not the 12-char abbreviation", rev)
	}
	if !strings.Contains(FullVersion(), rev) {
		t.Errorf("%q does not name the build it came from", FullVersion())
	}
}

// The toolchain's answer beats the build's, because the first cannot be stale
// and the second is a string somebody passed in.
func TestRecordedRevisionBeatsTheStampedOne(t *testing.T) {
	const recorded = "d70f0a230217e6a427941ddaff2bd0bf96e5f28e"
	const stamped = "ffffffffffffffffffffffffffffffffffffffff"
	for _, tc := range []struct{ recorded, stamped, want string }{
		{recorded, stamped, "d70f0a230217"}, // both: the toolchain wins
		{"", stamped, "ffffffffffff"},       // the image, whose context has no .git
		{recorded, "", "d70f0a230217"},      // a local build, which needs no help
		{"  ", stamped, "ffffffffffff"},     // blank is absent, not an answer
		{"", "", ""},                        // neither: say nothing rather than guess
	} {
		if got := pickRevision(tc.recorded, tc.stamped); got != tc.want {
			t.Errorf("pickRevision(%q, %q) = %q, want %q", tc.recorded, tc.stamped, got, tc.want)
		}
	}
}

func TestShortRevision(t *testing.T) {
	for in, want := range map[string]string{
		"d70f0a230217e6a427941ddaff2bd0bf96e5f28e":     "d70f0a230217",
		"  d70f0a230217e6a427941ddaff2bd0bf96e5f28e  ": "d70f0a230217",
		"short": "short",
		"":      "",
	} {
		if got := shortRevision(in); got != want {
			t.Errorf("shortRevision(%q) = %q, want %q", in, got, want)
		}
	}
}
