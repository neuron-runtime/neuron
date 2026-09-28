package contracts

import (
	"github.com/Muhammad-Jay/neuron/nore/internal/types"
	shared "github.com/Muhammad-Jay/neuron/shared/types/core"
)

type Compiler interface {
	Compile(assembly shared.Assembly) (*types.ExecutionBlueprint, error)
}
