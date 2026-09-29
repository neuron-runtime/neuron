package contracts

import (
	"github.com/neuron-runtime/neuron/nore/internal/types"
	shared "github.com/neuron-runtime/neuron/shared/types/core"
)

type Compiler interface {
	Compile(assembly shared.Assembly) (*types.ExecutionBlueprint, error)
}
