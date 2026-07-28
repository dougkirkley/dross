package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// shell is a background command started by the bash tool with background=true.
// Its output is captured for later reads and streamed as shell:output events.
type shell struct {
	id      string
	command string
	start   time.Time
	cmd     *exec.Cmd

	mu      sync.Mutex
	buf     bytes.Buffer
	done    bool
	exitErr error
}

func (s *shell) write(p []byte) {
	s.mu.Lock()
	s.buf.Write(p)
	s.mu.Unlock()
}

// status returns a snapshot: whether it's still running, its runtime, exit
// summary (empty while running), and the captured output so far.
func (s *shell) status() (running bool, runtime time.Duration, exit, output string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	running = !s.done
	runtime = time.Since(s.start).Round(time.Second)
	if s.done {
		exit = "exited 0"
		if s.exitErr != nil {
			exit = "exited: " + s.exitErr.Error()
		}
	}
	return running, runtime, exit, s.buf.String()
}

// shellEmitWriter streams a background shell's output as shell:output events so
// clients (the TUI) can show it live.
type shellEmitWriter struct {
	a  *Agent
	id string
}

func (w shellEmitWriter) Write(p []byte) (int, error) {
	w.a.emit("shell:output", w.id+workerTagSep+string(p))
	return len(p), nil
}

// startBackground launches command detached, registers it, and returns a note
// for the model. Output streams to shell:output events and is buffered for
// shell_output reads. The command is bound to ctx (killed when the run ends).
func (a *Agent) startBackground(ctx context.Context, command string) (string, bool) {
	a.shellMu.Lock()
	a.shellSeq++
	id := fmt.Sprintf("sh_%d", a.shellSeq)
	a.shellMu.Unlock()

	s := &shell{id: id, command: command, start: time.Now()}
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = a.ws.root
	sink := io.MultiWriter(writerFunc(s.write), shellEmitWriter{a: a, id: id})
	cmd.Stdout, cmd.Stderr = sink, sink
	s.cmd = cmd

	if err := cmd.Start(); err != nil {
		return "failed to start background shell: " + err.Error(), true
	}
	a.shellMu.Lock()
	a.shells = append(a.shells, s)
	a.shellMu.Unlock()
	a.emit("shell:start", id+workerTagSep+command)

	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.done, s.exitErr = true, err
		s.mu.Unlock()
		status := "exited 0"
		if err != nil {
			status = "exited: " + err.Error()
		}
		a.emit("shell:exit", id+workerTagSep+status)
	}()

	return fmt.Sprintf("started background shell %s: %s\nUse shell_output with id=%q to read its status and output; add kill=true to stop it.", id, command, id), false
}

// shellOutput reports a background shell's status + output, optionally killing it.
func (a *Agent) shellOutput(id string, kill bool) (string, bool) {
	s := a.findShell(id)
	if s == nil {
		return fmt.Sprintf("no background shell with id %q", id), true
	}
	if kill && s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	running, rt, exit, output := s.status()
	state := exit
	if running {
		state = "running"
	}
	header := fmt.Sprintf("shell %s — %s (%s)\n$ %s\n\n", id, state, rt, s.command)
	if output == "" {
		output = "[no output yet]"
	}
	return header + output, false
}

func (a *Agent) findShell(id string) *shell {
	a.shellMu.Lock()
	defer a.shellMu.Unlock()
	for _, s := range a.shells {
		if s.id == id {
			return s
		}
	}
	return nil
}

// writerFunc adapts a func to io.Writer.
type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) { f(p); return len(p), nil }
