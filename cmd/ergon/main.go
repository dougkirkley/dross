// Command ergon is a cost-aware, multi-model coding agent.
//
//	ergon agent  <prompt...>   run the agent once
//	ergon tui                  interactive terminal client
//	ergon serve                HTTP + SSE server (sessions, remote control)
//	ergon brief  [name]        print a handoff brief, or list sessions
//	ergon bench                benchmark vs Claude Code and Codex
package main

import (
	"fmt"
	"os"

	"github.com/dougkirkley/ergon/internal/cli"

	// Register model backends (driver-style; see internal/provider).
	_ "github.com/dougkirkley/ergon/pkg/provider/anthropic"
	_ "github.com/dougkirkley/ergon/pkg/provider/bedrock"
	_ "github.com/dougkirkley/ergon/pkg/provider/claudecode"
	_ "github.com/dougkirkley/ergon/pkg/provider/claudesub"
	_ "github.com/dougkirkley/ergon/pkg/provider/openai"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
