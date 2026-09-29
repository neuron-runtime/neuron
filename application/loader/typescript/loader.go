package typescript

import (
	"context"

	"github.com/neuron-runtime/neuron/application/process"
)

type TSLoader struct {
	process *process.Process
}

func NewTSLoader(cmd process.Command) *TSLoader {
	return &TSLoader{
		process: process.NewProcess(cmd),
	}
}

func (l *TSLoader) Build() error {
	return l.BuildContext(context.Background())
}

func (l *TSLoader) BuildContext(ctx context.Context) error {
	if _, err := l.process.RunContext(ctx); err != nil {
		return err
	}
	return nil
}
