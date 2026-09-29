package register

import (
	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/planner"
)

type Handler struct {
	instances  *instance.Manager
	assemblies *assembly.Repository
	compiler   *planner.Compiler
}

func New(instances *instance.Manager, assemblies *assembly.Repository, compiler *planner.Compiler) *Handler {
	return &Handler{instances: instances, assemblies: assemblies, compiler: compiler}
}
