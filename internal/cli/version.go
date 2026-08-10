package cli

import (
	"runtime/debug"
	"strings"
)

// What build is this?
//
// The version was a hand-maintained constant, which answers the question a
// release announcement asks and not the one anybody actually asks of a running
// process. A pod reporting "v0.4.0" is consistent with any commit made since
// somebody last thought to bump the constant — so "is the deployment current?"
// had no answer short of diffing behaviour.
//
// Go already knows. A build from a checkout carries its revision in the build
// info, for free, and it cannot drift because nobody maintains it. The one case
// it does not cover is the image, whose context excludes .git deliberately (it
// is large and the build does not need it), so that build passes the revision
// in instead.

// Version is the release string, bumped by hand, as it should be.
const Version = "v0.4.0"

// revision is the commit, for builds with no VCS information to read — the
// container image, whose context has no .git. Set with:
//
//	-ldflags "-X github.com/dougkirkley/dross/internal/cli.revision=$(git rev-parse HEAD)"
var revision string

// FullVersion is the release plus the commit it was built from, and whether the
// tree was dirty at the time.
func FullVersion() string {
	rev, dirty := buildRevision()
	if rev == "" {
		return Version
	}
	out := Version + " (" + rev
	if dirty {
		out += ", dirty"
	}
	return out + ")"
}

// buildRevision is what this binary was built from.
func buildRevision() (rev string, dirty bool) {
	var recorded string
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				recorded = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
	}
	return pickRevision(recorded, revision), dirty
}

// pickRevision prefers what the toolchain recorded over what a build passed in,
// because the first cannot be stale and the second is a string somebody typed.
//
// Split out to be testable: a `go test` binary carries no VCS information at
// all, so a test that reached for the real build info could only ever assert
// the fallback and would skip the case worth pinning down.
func pickRevision(recorded, stamped string) string {
	if strings.TrimSpace(recorded) != "" {
		return shortRevision(recorded)
	}
	return shortRevision(stamped)
}

// shortRevision is the abbreviation everything else in the toolchain prints, so
// a version line can be pasted into git log without editing.
func shortRevision(rev string) string {
	rev = strings.TrimSpace(rev)
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
