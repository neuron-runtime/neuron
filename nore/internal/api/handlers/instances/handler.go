package instances

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/nore/internal/planner"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

type Handler struct {
	instances  *instance.Manager
	assemblies *assembly.Repository
	compiler   *planner.Compiler
}

func New(m *instance.Manager, assemblies *assembly.Repository, compiler *planner.Compiler) *Handler {
	return &Handler{instances: m, assemblies: assemblies, compiler: compiler}
}

// resolveInstance maps a URL segment to a live instance. The segment may be an
// instance ID (inst_*) or a colon-encoded assembly key
// (assemblyID:version:hash[:env]); keys resolve to an existing runtime without
// creating one.
func (h *Handler) resolveInstance(r *http.Request, id string) (*instance.Instance, bool, error) {
	if strings.HasPrefix(id, "inst_") {
		i, ok := h.instances.GetByID(id)
		return i, ok, nil
	}
	key, err := protocol.ParseKey(id)
	if err != nil {
		return nil, false, nil
	}
	return h.instances.Resolve(r.Context(), key)
}

// runningInstance resolves the URL segment to an instance that is actually
// running. The segment is either an instance ID (inst_*) or a colon-encoded
// assembly key.
func (h *Handler) runningInstance(r *http.Request, id string) (*instance.Instance, error) {
	if strings.HasPrefix(id, "inst_") {
		i, ok := h.instances.GetByID(id)
		if !ok {
			return nil, fmt.Errorf("instance %s not found", id)
		}
		return h.runningInstanceByKey(r.Context(), i.Key)
	}

	key, err := protocol.ParseKey(id)
	if err != nil {
		return nil, fmt.Errorf("instance %s not found", id)
	}
	return h.runningInstanceByKey(r.Context(), key)
}

// runningInstanceByKey resolves a concrete assembly key to a running instance.
// A resolved instance whose runtime is gone — for example one restored
// metadata-only after a daemon restart — is transparently recreated from its
// registered key. This is what makes `neuron run` re-run an existing assembly
// instead of failing with "instance is not running".
func (h *Handler) runningInstanceByKey(ctx context.Context, key protocol.InstanceKey) (*instance.Instance, error) {
	i, ok, err := h.instances.Resolve(ctx, key)
	if err != nil {
		return nil, err
	}
	if ok && i.Status() == instance.StatusRunning {
		return i, nil
	}
	i, _, err = h.instances.GetOrCreate(ctx, key)
	return i, err
}
