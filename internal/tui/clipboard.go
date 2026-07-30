package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"regexp"

	"github.com/atotto/clipboard"
)

// ansiEscapeRE strips SGR/ANSI escapes so copied text is plain.
var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiEscapeRE.ReplaceAllString(s, "") }

// copyToClipboard writes text to the system clipboard two ways, so it works
// both locally and over SSH — like Crush: the native OS clipboard (atotto shells
// out to pbcopy/xclip/wl-copy, no cgo) and an OSC 52 escape the terminal picks
// up for remote sessions. Errors are non-fatal (best effort).
func copyToClipboard(text string) {
	_ = clipboard.WriteAll(text)
	// OSC 52: ESC ] 52 ; c ; <base64> BEL — written to the tty (stderr) so it is
	// intercepted by the terminal, not the alt-screen buffer.
	b64 := base64.StdEncoding.EncodeToString([]byte(text))
	fmt.Fprintf(os.Stderr, "\x1b]52;c;%s\a", b64)
}
