package instances

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Muhammad-Jay/neuron/nore/internal/instance"
	"github.com/Muhammad-Jay/neuron/nore/internal/planner"
	"github.com/Muhammad-Jay/neuron/nore/internal/system"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

type Handler struct {
	instances *instance.Manager
	systems   *system.Repository
	compiler  *planner.Compiler
}

func New(m *instance.Manager, systems *system.Repository, compiler *planner.Compiler) *Handler {
	return &Handler{instances: m, systems: systems, compiler: compiler}
}

// resolveInstance maps a URL segment to a live instance. The segment may be an
// instance ID (inst_*) or a colon-encoded system key
// (systemID:version:hash[:env]); keys resolve to an existing runtime without
// creating one.
func (h *Handler) resolveInstance(r *http.Request, id string) (*instance.Instance, bool) {
	if strings.HasPrefix(id, "inst_") {
		return h.instances.GetByID(id)
	}
	key, err := protocol.ParseKey(id)
	if err != nil {
		return nil, false
	}
	return h.instances.Resolve(r.Context(), key)
}

// runningInstance resolves the URL segment to an instance that is actually
// running. A resolved instance whose runtime is gone — for example one restored
// metadata-only after a daemon restart — is transparently recreated from its
// registered key. This is what makes `neuron run` re-run an existing system
// instead of failing with "instance is not running".
func (h *Handler) runningInstance(r *http.Request, id string) (*instance.Instance, error) {
	if i, ok := h.resolveInstance(r, id); ok {
		if i.Status() == instance.StatusRunning {
			return i, nil
		}
		// Stale runtime: recreate it under the same key.
		recreated, _, err := h.instances.GetOrCreate(r.Context(), i.Key)
		return recreated, err
	}

	var key protocol.InstanceKey
	if strings.HasPrefix(id, "inst_") {
		i, ok := h.instances.GetByID(id)
		if !ok {
			return nil, fmt.Errorf("instance %s not found", id)
		}
		key = i.Key
	} else {
		parsed, err := protocol.ParseKey(id)
		if err != nil {
			return nil, fmt.Errorf("instance %s not found", id)
		}
		key = parsed
	}
	i, _, err := h.instances.GetOrCreate(r.Context(), key)
	return i, err
}
