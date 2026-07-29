package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
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

// BackgroundForeground converts the currently-running foreground bash command,
// if any, into a background shell — the TUI calls this when the user presses
// ctrl+b. It is safe to call anytime and is a no-op when no foreground command
// is waiting to be converted.
func (a *Agent) BackgroundForeground() {
	select {
	case a.bgReq <- struct{}{}:
	default:
	}
}

// fgSink captures a convertible foreground command's output into its buffer and,
// once the command has been adopted into the background registry (id set), also
// streams it as shell:output events so the shells viewer stays live.
type fgSink struct {
	a *Agent
	s *shell
}

func (w fgSink) Write(p []byte) (int, error) {
	w.s.mu.Lock()
	w.s.buf.Write(p)
	id := w.s.id
	w.s.mu.Unlock()
	if id != "" {
		w.a.emit("shell:output", id+workerTagSep+string(p))
	}
	return len(p), nil
}

// runForegroundBash runs command synchronously, but lets the user convert it
// into a background shell mid-run (BackgroundForeground, bound to ctrl+b). On
// conversion the still-running command is registered in the shell registry and a
// note is returned so the agent loop continues; otherwise it blocks until the
// command finishes or bashTimeout elapses.
func (a *Agent) runForegroundBash(ctx context.Context, command string) (string, bool) {
	// Bind to a child of the run context so a backgrounded command outlives the
	// foreground timeout yet still dies when the whole run ends.
	childCtx, cancel := context.WithCancel(ctx)

	s := &shell{command: command, start: time.Now()}
	cmd := exec.CommandContext(childCtx, "bash", "-c", command)
	cmd.Dir = a.ws.root
	sink := fgSink{a: a, s: s}
	cmd.Stdout, cmd.Stderr = sink, sink
	s.cmd = cmd

	if err := cmd.Start(); err != nil {
		cancel()
		return "bash: " + err.Error(), true
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timeout := time.NewTimer(bashTimeout)
	defer timeout.Stop()

	for {
		select {
		case err := <-done:
			cancel()
			return foregroundBashResult(s.buf.String(), err), false
		case <-timeout.C:
			cancel() // kills the process via childCtx
			<-done
			return fmt.Sprintf("%s\n[command timed out after %s]", s.buf.String(), bashTimeout), false
		case <-a.bgReq:
			return a.adoptBackground(s, done, cancel), false
		}
	}
}

// adoptBackground registers an already-running foreground command as a background
// shell and spawns its exit watcher, returning the note handed back to the model.
func (a *Agent) adoptBackground(s *shell, done chan error, cancel context.CancelFunc) string {
	a.shellMu.Lock()
	a.shellSeq++
	id := fmt.Sprintf("sh_%d", a.shellSeq)
	s.mu.Lock()
	s.id = id
	buffered := s.buf.String()
	s.mu.Unlock()
	a.shells = append(a.shells, s)
	a.shellMu.Unlock()

	a.emit("shell:start", id+workerTagSep+s.command)
	if buffered != "" { // replay pre-conversion output so the viewer isn't empty
		a.emit("shell:output", id+workerTagSep+buffered)
	}

	go func() {
		err := <-done
		s.mu.Lock()
		s.done, s.exitErr = true, err
		s.mu.Unlock()
		cancel()
		status := "exited 0"
		if err != nil {
			status = "exited: " + err.Error()
		}
		a.emit("shell:exit", id+workerTagSep+status)
	}()

	return fmt.Sprintf("moved to background as %s (was running in the foreground); use shell_output with id=%q to read its status and output.", id, id)
}

// foregroundBashResult formats a finished foreground command's output the same
// way runBash does: non-zero exit is information for the model, not a failure.
func foregroundBashResult(out string, err error) string {
	if err != nil {
		return fmt.Sprintf("%s\n[exit status: %v]", out, err)
	}
	if strings.TrimSpace(out) == "" {
		return "[no output]"
	}
	return out
}
