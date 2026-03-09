package worker

import "context"

type OutputType string

const (
	OutputStdout OutputType = "stdout"
	OutputStderr OutputType = "stderr"
	OutputDone   OutputType = "done"
	OutputError  OutputType = "error"
)

type Output struct {
	Type    OutputType `json:"type"`
	Content string     `json:"content"`
}

type ExecuteOptions struct {
	SessionID string // passed to Claude CLI via --session-id or --resume
	Resume    bool   // if true, use --resume; if false, use --session-id
}

type Runtime interface {
	Execute(ctx context.Context, workDir string, plan string, opts ExecuteOptions) (<-chan Output, error)
	PID() int
	Stop() error
}
