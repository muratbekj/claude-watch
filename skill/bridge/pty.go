package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// ptyProcess wraps a child process spawned either interactively (wrapped
// in `script` to fake a tty, matching server.js's spawnInteractiveProcess)
// or headlessly (the one-shot `claude -p ... --continue` fallback).
type ptyProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser // nil for headless processes
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// mergeEnv overrides keys in the current environment, ensuring the
// override wins regardless of lookup order in the child process.
func mergeEnv(overrides map[string]string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if _, skip := overrides[key]; skip {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// spawnInteractiveProcess mirrors server.js exactly: it shells out to the
// Unix `script` utility (`script -q /dev/null <bin> [...args]`) to give
// the child a real tty, rather than using a pty library.
func spawnInteractiveProcess(bin, cwd string, args []string) (*ptyProcess, error) {
	if bin == "" {
		return nil, fmt.Errorf("binary not found")
	}
	cols := envInt("COLUMNS", 120)
	rows := envInt("LINES", 40)

	fullArgs := append([]string{"-q", "/dev/null", bin}, args...)
	cmd := exec.Command("script", fullArgs...)
	cmd.Dir = cwd
	cmd.Env = mergeEnv(map[string]string{
		"TERM":    "xterm-256color",
		"COLUMNS": strconv.Itoa(cols),
		"LINES":   strconv.Itoa(rows),
	})

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &ptyProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

// spawnHeadless runs a binary directly (no `script` wrapping, no stdin) —
// used for the one-shot `claude -p "<prompt>" --continue` fallback when a
// session has no bridge-owned PTY.
func spawnHeadless(bin string, args []string, cwd string) (*ptyProcess, error) {
	if bin == "" {
		return nil, fmt.Errorf("binary not found")
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &ptyProcess{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

func (p *ptyProcess) pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *ptyProcess) writeStdin(s string) error {
	if p.stdin == nil {
		return fmt.Errorf("session has no live stdin")
	}
	_, err := io.WriteString(p.stdin, s)
	return err
}

// terminate mirrors Node's default ChildProcess.kill() behavior (SIGTERM).
func (p *ptyProcess) terminate() {
	if p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
}

// streamAndWait reads stdout/stderr as raw byte chunks (not line-buffered,
// matching Node's 'data' event semantics) until EOF, then waits for exit.
// onChunk's isStderr flag lets callers apply stream-specific filtering
// (the headless prompt path filters noisy "tcgetattr" stderr lines).
func (p *ptyProcess) streamAndWait(onChunk func(text string, isStderr bool)) (exitCode int, signal string, spawnErr error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go readChunks(p.stdout, false, onChunk, &wg)
	go readChunks(p.stderr, true, onChunk, &wg)
	wg.Wait()

	err := p.cmd.Wait()
	if state := p.cmd.ProcessState; state != nil {
		exitCode = state.ExitCode()
		if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			signal = ws.Signal().String()
		}
	}
	if err != nil {
		if _, isExitErr := err.(*exec.ExitError); !isExitErr {
			spawnErr = err
		}
	}
	return
}

func readChunks(r io.Reader, isStderr bool, onChunk func(string, bool), wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			onChunk(string(buf[:n]), isStderr)
		}
		if err != nil {
			return
		}
	}
}
