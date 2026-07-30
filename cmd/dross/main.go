// Command dross is a cost-aware, multi-model coding agent.
//
//	dross agent  <prompt...>   run the agent once
//	dross tui                  interactive terminal client
//	dross serve                HTTP + SSE server (sessions, remote control)
//	dross brief  [name]        print a handoff brief, or list sessions
//	dross bench                benchmark vs Claude Code and Codex
package main

import (
	"fmt"
	"os"

	"github.com/dougkirkley/dross/internal/cli"

	// Register model backends (driver-style; see internal/provider).
	_ "github.com/dougkirkley/dross/pkg/provider/anthropic"
	_ "github.com/dougkirkley/dross/pkg/provider/azure"
	_ "github.com/dougkirkley/dross/pkg/provider/bedrock"
	_ "github.com/dougkirkley/dross/pkg/provider/claudecode"
	_ "github.com/dougkirkley/dross/pkg/provider/claudesub"
	_ "github.com/dougkirkley/dross/pkg/provider/gemini"
	_ "github.com/dougkirkley/dross/pkg/provider/geminicli"
	_ "github.com/dougkirkley/dross/pkg/provider/openai"
	_ "github.com/dougkirkley/dross/pkg/provider/vertex"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
