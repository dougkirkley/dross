package tui

import (
	"strings"
	"unicode"
)

// Keyboard mash detection: catching "a cat walked across the keyboard" input
// before it is sent as a prompt, so a stray elbow doesn't burn a model turn.
//
// The bar is deliberately high: a false positive blocks real work, while a false
// negative only costs what it costs today. Anything containing a plausible word,
// a path, a flag, or code punctuation is left alone.

// qwertyRows are the physical key rows used to score "adjacent key" runs, the
// signature of a hand dragged across the keyboard.
var qwertyRows = []string{"qwertyuiop", "asdfghjkl", "zxcvbnm"}

// keyPos maps a letter to its (row, column) on a QWERTY keyboard.
var keyPos = func() map[rune][2]int {
	m := make(map[rune][2]int)
	for r, row := range qwertyRows {
		for c, ch := range row {
			m[ch] = [2]int{r, c}
		}
	}
	return m
}()

// longestRowRun returns the longest run of letters that step one key at a time
// in a single direction along one keyboard row — "asdfg", "qwerty", "fdsa".
// A loose "keys are near each other" test flags real words (rhythm, strength);
// requiring a straight, monotonic drag does not.
func longestRowRun(runes []rune) int {
	best, run, dir := 1, 1, 0
	for i := 1; i < len(runes); i++ {
		pa, ok1 := keyPos[runes[i-1]]
		pb, ok2 := keyPos[runes[i]]
		step := 0
		if ok1 && ok2 && pa[0] == pb[0] {
			step = pb[1] - pa[1]
		}
		if step == 1 || step == -1 {
			if step == dir {
				run++
			} else {
				run, dir = 2, step
			}
			if run > best {
				best = run
			}
		} else {
			run, dir = 1, 0
		}
	}
	return best
}

func hasVowel(s string) bool {
	return strings.ContainsAny(s, "aeiouyAEIOUY")
}

// looksLikeMash reports whether s looks like accidental keyboard input rather
// than a real prompt.
func looksLikeMash(s string) bool {
	t := strings.TrimSpace(s)
	if len([]rune(t)) < 6 { // short input is usually deliberate ("hi", "go", "ls")
		return false
	}
	// Anything that looks like code, a path, a flag, or a command is real input.
	if strings.ContainsAny(t, "/\\.:_-=<>(){}[]$#@|*\"'`") {
		return false
	}
	// Digits are rare in a mash and common in real prompts (versions, counts).
	for _, r := range t {
		if unicode.IsDigit(r) {
			return false
		}
	}
	fields := strings.Fields(t)
	if len(fields) > 4 { // several words: almost certainly a real sentence
		return false
	}
	// Every token must look like gibberish for the whole input to count.
	for _, w := range fields {
		if !tokenIsMash(w) {
			return false
		}
	}
	return len(fields) > 0
}

// tokenIsMash scores a single word.
func tokenIsMash(w string) bool {
	lw := strings.ToLower(w)
	runes := []rune(lw)
	for _, r := range runes {
		if !unicode.IsLetter(r) { // punctuation/symbols: treat as intentional
			return false
		}
	}
	if len(runes) < 4 {
		return false // short words are real often enough ("the", "fix", "go")
	}

	// A long run of one character ("aaaaaaa", "zzzzzz").
	run, maxRun := 1, 1
	for i := 1; i < len(runes); i++ {
		if runes[i] == runes[i-1] {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 1
		}
	}
	if maxRun >= 4 {
		return true
	}

	// No vowel at all in a long token ("sdfghjkl", "kjhgfds").
	if len(runes) >= 5 && !hasVowel(lw) {
		return true
	}

	// A hand dragged straight along a key row ("asdfgh", "qwerty", "fdsa").
	return longestRowRun(runes) >= 4
}
