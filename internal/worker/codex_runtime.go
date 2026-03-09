package worker

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"sync"
)

type CodexRuntime struct {
	binary string
	cmd    *exec.Cmd
	mu     sync.Mutex
}

func NewCodexRuntime(binary string) *CodexRuntime {
	return &CodexRuntime{binary: binary}
}

func (r *CodexRuntime) Execute(ctx context.Context, workDir string, plan string, opts ExecuteOptions) (<-chan Output, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cmd = exec.CommandContext(ctx, r.binary, buildCodexArgs(plan)...)
	r.cmd.Dir = workDir

	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := r.cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := r.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex: %w", err)
	}

	ch := make(chan Output, 100)

	go func() {
		defer close(ch)

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				ch <- Output{Type: OutputStdout, Content: scanner.Text()}
			}
		}()

		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stderr)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				ch <- Output{Type: OutputStderr, Content: scanner.Text()}
			}
		}()

		wg.Wait()

		if err := r.cmd.Wait(); err != nil {
			ch <- Output{Type: OutputError, Content: err.Error()}
		} else {
			ch <- Output{Type: OutputDone, Content: ""}
		}
	}()

	return ch, nil
}

func buildCodexArgs(plan string) []string {
	return []string{
		"exec",
		"--full-auto",
		"--skip-git-repo-check",
		plan,
	}
}

func (r *CodexRuntime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Kill()
	}
	return nil
}

func (r *CodexRuntime) PID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Pid
	}
	return 0
}
