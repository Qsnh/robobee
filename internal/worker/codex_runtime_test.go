package worker

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestBuildCodexArgs(t *testing.T) {
	got := buildCodexArgs("run tests")
	want := []string{"exec", "--full-auto", "--skip-git-repo-check", "run tests"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected codex args: got %v, want %v", got, want)
	}
}

func TestCodexRuntimeExecuteUsesExecArgs(t *testing.T) {
	r := NewCodexRuntime("echo")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	outputCh, err := r.Execute(ctx, t.TempDir(), "hello", ExecuteOptions{})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	var stdout string
	done := false

	for out := range outputCh {
		switch out.Type {
		case OutputStdout:
			stdout = out.Content
		case OutputDone:
			done = true
		case OutputError:
			t.Fatalf("unexpected runtime error: %s", out.Content)
		}
	}

	if !done {
		t.Fatal("expected done output event")
	}

	want := "exec --full-auto --skip-git-repo-check hello"
	if stdout != want {
		t.Fatalf("unexpected stdout: got %q, want %q", stdout, want)
	}
}
