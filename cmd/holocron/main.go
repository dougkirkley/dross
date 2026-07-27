// Command holocron is a cost-aware, multi-model coding agent.
//
//	holocron agent  <prompt...>   run the agent once
//	holocron tui                  interactive terminal client
//	holocron serve                HTTP + SSE server (sessions, remote control)
//	holocron brief  [name]        print a handoff brief, or list sessions
//	holocron bench                benchmark vs Claude Code and Codex
package main

import (
	"fmt"
	"os"

	"github.com/dougkirkley/holocron/internal/cli"

	// Register model backends (driver-style; see internal/provider).
	_ "github.com/dougkirkley/holocron/internal/provider/anthropic"
	_ "github.com/dougkirkley/holocron/internal/provider/openai"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
