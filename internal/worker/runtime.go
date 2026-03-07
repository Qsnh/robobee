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

type Runtime interface {
	Execute(ctx context.Context, workDir string, plan string) (<-chan Output, error)
	Stop() error
}
