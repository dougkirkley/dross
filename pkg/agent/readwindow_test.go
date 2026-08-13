package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dougkirkley/dross/pkg/provider"
)

// numberedFile is a file whose every line says which line it is, so a slice of
// it can be checked without counting.
func numberedFile(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

// TestReadSliceNumbersLinesAsTheyAreInTheFile: the numbers are the file's, not
// the slice's. A slice numbered from 1 is worse than no numbers at all — every
// line it names is wrong by the offset, and an edit aimed at one of them lands
// somewhere else.
func TestReadSliceNumbersLinesAsTheyAreInTheFile(t *testing.T) {
	w, _ := ws(t, map[string]string{"big.txt": numberedFile(3000)})

	out, err := w.readFile("big.txt", readWindow{Offset: 2400, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  2400\tline 2400") {
		t.Errorf("slice should start at 2400 and say so:\n%s", out)
	}
	if strings.Contains(out, "line 2399") || strings.Contains(out, "line 2403") {
		t.Errorf("slice returned lines outside the window:\n%s", out)
	}
	if strings.Contains(out, "     1\t") {
		t.Errorf("slice must not renumber from 1:\n%s", out)
	}
}

// TestReadSliceSaysWhatIsLeft: a slice that stops without saying what follows
// it is how a run concludes it has read a file it has read a fifth of. The
// footer carries both facts — how much remains, and the offset that continues.
func TestReadSliceSaysWhatIsLeft(t *testing.T) {
	w, _ := ws(t, map[string]string{"big.txt": numberedFile(1000)})

	out, err := w.readFile("big.txt", readWindow{Limit: 400})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "600 more lines") {
		t.Errorf("footer should count what is left:\n%s", tail(out))
	}
	if !strings.Contains(out, "offset=401") {
		t.Errorf("footer should say where to continue:\n%s", tail(out))
	}
}

// TestReadSliceThatEndsTheFileSaysNothing: the footer is for a slice with more
// behind it. On the last one it would be a lie the model then acts on.
func TestReadSliceThatEndsTheFileSaysNothing(t *testing.T) {
	w, _ := ws(t, map[string]string{"small.txt": numberedFile(10)})

	out, err := w.readFile("small.txt", readWindow{Offset: 5, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "more lines") {
		t.Errorf("nothing follows line 10; footer should be absent:\n%s", out)
	}
	if !strings.Contains(out, "    10\tline 10") {
		t.Errorf("a limit past the end returns what there is:\n%s", out)
	}
}

// TestReadTooBigToReadWhole is the round trip this removes: the refusal used to
// send the model to bash for a sed one-liner, which costs a turn and comes back
// without line numbers. Now the refusal names the parameters that work, and
// they do work on the same file.
func TestReadTooBigToReadWhole(t *testing.T) {
	big := strings.Repeat("a line of filler text to make this file large\n", 8000)
	w, _ := ws(t, map[string]string{"huge.txt": big})

	_, err := w.readFile("huge.txt", readWindow{})
	if err == nil {
		t.Fatal("a file over the byte limit should not be read whole")
	}
	for _, want := range []string{"offset", "limit", "grep"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %q as the way on: %v", want, err)
		}
	}

	out, err := w.readFile("huge.txt", readWindow{Offset: 100, Limit: 5})
	if err != nil {
		t.Fatalf("the same file must be readable in slices: %v", err)
	}
	if !strings.Contains(out, "   100\t") || !strings.Contains(out, "   104\t") {
		t.Errorf("wanted lines 100-104:\n%s", out)
	}
	if strings.Contains(out, "   105\t") {
		t.Errorf("limit 5 returned more than five lines:\n%s", out)
	}
}

// TestReadSliceStaysUnderTheByteLimit: an offset with no limit on a very large
// file must not hand back the file. It stops, and says it stopped.
func TestReadSliceStaysUnderTheByteLimit(t *testing.T) {
	line := strings.Repeat("x", 500) + "\n"
	w, _ := ws(t, map[string]string{"wide.txt": strings.Repeat(line, 3000)})

	out, err := w.readFile("wide.txt", readWindow{Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > maxReadBytes+1024 {
		t.Errorf("slice is %d bytes, over the %d budget", len(out), maxReadBytes)
	}
	if !strings.Contains(out, "byte limit") {
		t.Errorf("a slice cut short by size should say so:\n%s", tail(out))
	}
	if !strings.Contains(out, "more lines") {
		t.Errorf("and should still say what is left:\n%s", tail(out))
	}
}

// TestReadTailTakesTheEnd is the route a traced run could not take: it walked a
// 300 KB log from line 1 in eight slices and ran out of turns short of the
// answer, which was in the last six lines. An offset has to be a line number,
// and the last line number of an unread file is unknowable — so the end was the
// one place the tool could not go.
func TestReadTailTakesTheEnd(t *testing.T) {
	w, _ := ws(t, map[string]string{"run.log": numberedFile(4000) + "FATAL out of retries\n"})

	out, err := w.readFile("run.log", readWindow{Offset: -3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "FATAL out of retries") {
		t.Errorf("the tail should hold the last line:\n%s", out)
	}
	if !strings.Contains(out, "  4001\t") {
		t.Errorf("tail lines keep their real numbers:\n%s", out)
	}
	if strings.Contains(out, "line 3997") {
		t.Errorf("asked for 3 lines, got more:\n%s", out)
	}
	// And it says what it is the tail of, or the run cannot ask for the rest.
	if !strings.Contains(out, "3998 lines come before") {
		t.Errorf("the header should place the tail in the file:\n%s", out)
	}
}

// TestReadTailShorterThanTheFileWants: asking for more end than there is file
// is the whole file, not an error — the run does not know the length, which is
// why it asked this way.
func TestReadTailShorterThanTheFileWants(t *testing.T) {
	w, _ := ws(t, map[string]string{"small.txt": numberedFile(4)})

	out, err := w.readFile("small.txt", readWindow{Offset: -200})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "     1\tline 1") || !strings.Contains(out, "     4\tline 4") {
		t.Errorf("a short file tails to itself:\n%s", out)
	}
	if strings.Contains(out, "come before") {
		t.Errorf("nothing comes before line 1; the header should be absent:\n%s", out)
	}
}

// TestReadTailKeepsTheNewestUnderBudget: a tail too big for the budget must
// lose its oldest lines. Losing the newest would hand back everything except
// the part that was asked for.
func TestReadTailKeepsTheNewestUnderBudget(t *testing.T) {
	line := strings.Repeat("x", 500) + "\n"
	w, _ := ws(t, map[string]string{"wide.log": strings.Repeat(line, 2000) + "THE LAST LINE\n"})

	out, err := w.readFile("wide.log", readWindow{Offset: -2000})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > maxReadBytes+1024 {
		t.Errorf("tail is %d bytes, over the %d budget", len(out), maxReadBytes)
	}
	if !strings.Contains(out, "THE LAST LINE") {
		t.Error("the budget cut the end off a tail, which is the part that was asked for")
	}
}

// TestBigFileRefusalLeadsWithSearch pins the order of the refusal's advice,
// which is not cosmetic. It used to read "read it in slices (offset 1 limit
// 500, then offset 501), or grep it" — and a traced run did exactly that,
// literally, eight times, down a file whose answer was at the other end.
func TestBigFileRefusalLeadsWithSearch(t *testing.T) {
	big := strings.Repeat("a line of filler text to make this file large\n", 8000)
	w, _ := ws(t, map[string]string{"huge.log": big})

	_, err := w.readFile("huge.log", readWindow{})
	if err == nil {
		t.Fatal("a file over the byte limit should not be read whole")
	}
	msg := err.Error()
	grep, tail, walk := strings.Index(msg, "grep"), strings.Index(msg, "-200"), strings.Index(msg, "slices")
	if grep < 0 || tail < 0 || walk < 0 {
		t.Fatalf("the refusal should offer all three routes: %s", msg)
	}
	if !(grep < tail && tail < walk) {
		t.Errorf("cheapest route first: want grep, then the tail, then the walk:\n%s", msg)
	}
}

// TestReadOffsetPastTheEnd: the error says how long the file is, so the next
// call is a corrected read rather than a second guess.
func TestReadOffsetPastTheEnd(t *testing.T) {
	w, _ := ws(t, map[string]string{"short.txt": numberedFile(12)})

	_, err := w.readFile("short.txt", readWindow{Offset: 900, Limit: 10})
	if err == nil {
		t.Fatal("an offset past the end is not a read")
	}
	if !strings.Contains(err.Error(), "12 lines") {
		t.Errorf("the error should say how long the file is: %v", err)
	}
}

// TestReadWindowLeavesWholeFileReadsAlone: every existing caller passes an
// empty window, and their output must be byte-for-byte what it was.
func TestReadWindowLeavesWholeFileReadsAlone(t *testing.T) {
	w, _ := ws(t, map[string]string{"a.go": "package a\n\nfunc A() {}\n"})

	out, err := w.readFile("a.go", readWindow{})
	if err != nil {
		t.Fatal(err)
	}
	want := "     1\tpackage a\n     2\t\n     3\tfunc A() {}\n     4\t\n"
	if out != want {
		t.Errorf("whole-file read changed shape:\ngot  %q\nwant %q", out, want)
	}
}

// TestReadWindowAppliesToEveryFile: the window is the call's, not the first
// path's — a batch that asked for the first forty lines of three files gets
// three heads, not one head and two whole files.
func TestReadWindowAppliesToEveryFile(t *testing.T) {
	w, _ := ws(t, map[string]string{
		"a.txt": numberedFile(50),
		"b.txt": numberedFile(50),
	})

	out, err := w.readFiles([]string{"a.txt", "b.txt"}, readWindow{Offset: 1, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "line 3"); n != 0 {
		t.Errorf("limit 2 leaked line 3 into %d file(s):\n%s", n, out)
	}
	if n := strings.Count(out, "line 1\n"); n != 2 {
		t.Errorf("both files should be sliced, got %d:\n%s", n, out)
	}
}

// TestReadToolTakesOffsetAndLimit walks the whole path: the schema advertises
// them, the dispatch parses them, and the model gets back the lines it asked
// for rather than the file they are in.
func TestReadToolTakesOffsetAndLimit(t *testing.T) {
	_, dir := ws(t, map[string]string{"big.txt": numberedFile(500)})
	prov := &scriptedProvider{steps: []provider.Response{
		{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall("1", "read", map[string]any{"path": "big.txt", "offset": 300, "limit": 2}),
		}},
		{StopReason: provider.StopEndTurn, Text: "read it"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), "read a slice"); err != nil {
		t.Fatal(err)
	}
	last := prov.last.seen()
	if !strings.Contains(last, "   300\tline 300") {
		t.Fatalf("the window did not reach the read:\n%s", last)
	}
	if strings.Contains(last, "line 302") {
		t.Fatalf("limit 2 was ignored:\n%s", last)
	}

	for _, def := range toolDefs(false, false, false, false) {
		if def.Name != "read" {
			continue
		}
		for _, prop := range []string{"offset", "limit"} {
			if _, ok := def.Properties[prop]; !ok {
				t.Errorf("read schema is missing %q, so a model has no way to know it exists", prop)
			}
		}
	}
}

// TestWalkHintArrivesOnTheThirdSlice is the second line of defence against the
// traced failure: eight slices down a log from line 1, both cheaper routes
// available the whole time and neither tried. It arrives once the walk is a
// habit rather than a look, and says both ways out.
func TestWalkHintArrivesOnTheThirdSlice(t *testing.T) {
	_, dir := ws(t, map[string]string{"run.log": numberedFile(4000)})
	slice := func(id string, offset int) provider.Response {
		return provider.Response{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall(id, "read", map[string]any{"path": "run.log", "offset": offset, "limit": 500}),
		}}
	}
	prov := &scriptedProvider{steps: []provider.Response{
		slice("1", 1), slice("2", 501), slice("3", 1001),
		{StopReason: provider.StopEndTurn, Text: "read it"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	ag.SetEventHook(func(e Event) {
		if e.Kind == "tool-result" {
			results = append(results, e.Data)
		}
	})
	if _, err := ag.Run(context.Background(), "what died"); err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	// Not on the first two: a section and the next one is reading, not a habit.
	for i := 0; i < 2; i++ {
		if strings.Contains(results[i], "offset=-200") {
			t.Errorf("slice %d was hinted too early:\n%s", i+1, tail(results[i]))
		}
	}
	third := results[2]
	if !strings.Contains(third, "offset=-200") {
		t.Errorf("the third slice should offer the tail:\n%s", tail(third))
	}
	if !strings.Contains(third, "grep") {
		t.Errorf("and should offer the search:\n%s", tail(third))
	}
}

// TestTailIsNotHintedAsAWalk: a negative offset is the shortcut the hint
// recommends. Counting it as a walk would nag a run for taking the advice.
func TestTailIsNotHintedAsAWalk(t *testing.T) {
	_, dir := ws(t, map[string]string{"run.log": numberedFile(4000)})
	tailCall := func(id string) provider.Response {
		return provider.Response{StopReason: provider.StopToolUse, ToolCalls: []provider.ToolCall{
			toolCall(id, "read", map[string]any{"path": "run.log", "offset": -100}),
		}}
	}
	prov := &scriptedProvider{steps: []provider.Response{
		tailCall("1"), tailCall("2"), tailCall("3"),
		{StopReason: provider.StopEndTurn, Text: "read it"},
	}}
	ag, err := New(Config{Name: "t", Model: "m", Workdir: dir}, prov)
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	ag.SetEventHook(func(e Event) {
		if e.Kind == "tool-result" {
			results = append(results, e.Data)
		}
	})
	if _, err := ag.Run(context.Background(), "what died"); err != nil {
		t.Fatal(err)
	}
	for i, r := range results {
		if strings.Contains(r, "working down it from the top") {
			t.Errorf("tail %d was counted as a walk:\n%s", i+1, tail(r))
		}
	}
}

// tail keeps a failure message about a footer to the part it is about.
func tail(s string) string {
	if len(s) < 400 {
		return s
	}
	return "…" + s[len(s)-400:]
}
