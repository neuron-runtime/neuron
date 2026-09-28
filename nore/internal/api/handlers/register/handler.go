package register

import (
	"github.com/Muhammad-Jay/neuron/nore/internal/instance"
	"github.com/Muhammad-Jay/neuron/nore/internal/planner"
	"github.com/Muhammad-Jay/neuron/nore/internal/assembly"
)

type Handler struct {
	instances *instance.Manager
	assemblies   *assembly.Repository
	compiler  *planner.Compiler
}

func New(instances *instance.Manager, assemblies *assembly.Repository, compiler *planner.Compiler) *Handler {
	return &Handler{instances: instances, assemblies: assemblies, compiler: compiler}
}
