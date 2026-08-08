// Probe: trace what gpt-oss:20b actually does on the tasks where it burns
// turns, one line per tool call and result. The bench says 12 turns without
// saying what they were spent on.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dougkirkley/dross/internal/bench"
	"github.com/dougkirkley/dross/pkg/agent"
	"github.com/dougkirkley/dross/pkg/provider"
	_ "github.com/dougkirkley/dross/pkg/provider/claudecode"
	_ "github.com/dougkirkley/dross/pkg/provider/openai"
)

func main() {
	want := map[string]bool{}
	for _, n := range os.Args[1:] {
		want[n] = true
	}
	for _, t := range bench.DefaultTasks() {
		if len(want) > 0 && !want[t.Name] {
			continue
		}
		run(t)
	}
}

func run(task bench.Task) {
	dir, err := os.MkdirTemp("", "probe")
	must(err)
	defer os.RemoveAll(dir)
	for name, body := range task.Files {
		must(os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
		must(os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}

	prov, err := provider.New("openai", provider.Config{BaseURL: "http://localhost:11434/v1"})
	must(err)

	turn := 0
	ag, err := agent.New(agent.Config{
		Name: "probe", Model: "gpt-oss:20b", Workdir: dir, MaxTurns: 40,
	}, prov, agent.WithEventHook(func(e agent.Event) {
		switch e.Kind {
		case "usage":
			turn++
			fmt.Printf("  --- turn %d\n", turn)
		case "tool":
			fmt.Printf("      call: %s\n", trunc(e.Data, 160))
		case "tool-result":
			fmt.Printf("        => %s\n", trunc(oneLine(e.Data), 160))
		case "edit":
			fmt.Printf("        edit: %s\n", trunc(oneLine(e.Data), 160))
		case "text":
			fmt.Printf("      text: %s\n", trunc(oneLine(e.Data), 160))
		case "error":
			fmt.Printf("      ERROR: %s\n", trunc(oneLine(e.Data), 200))
		}
	}))
	must(err)
	defer ag.Close()

	fmt.Printf("=== %s\n", task.Name)
	start := time.Now()
	res, err := ag.Run(context.Background(), task.Prompt)
	ag.Wait()
	wall := time.Since(start).Round(time.Millisecond)
	if err != nil {
		fmt.Printf("  RUN ERROR: %v\n", err)
	}
	check := exec.Command(task.Check[0], task.Check[1:]...)
	check.Dir = dir
	status := "PASS"
	if err := check.Run(); err != nil {
		status = "FAIL"
	}
	turns := 0
	if res != nil {
		turns = res.Turns
	}
	fmt.Printf("  %s %s turns=%d\n\n", status, wall, turns)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe:", err)
		os.Exit(1)
	}
}
