package typescript

import (
	"context"
	"fmt"
	"strings"

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
	output, err := l.process.RunContext(ctx)
	if err != nil {
		diagnostic := strings.TrimSpace(output)
		if diagnostic == "" {
			return fmt.Errorf("run TypeScript SDK build: %w", err)
		}
		return fmt.Errorf("run TypeScript SDK build: %w\nSDK output:\n%s", err, diagnostic)
	}
	return nil
}
