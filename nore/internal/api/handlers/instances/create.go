package instances

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Muhammad-Jay/neuron/nore/internal/api/utils"
	"github.com/Muhammad-Jay/neuron/nore/internal/storage"
	"github.com/Muhammad-Jay/neuron/nore/internal/assembly"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

func (h *Handler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	// 2. Use the protocol struct instead of an anonymous struct
	var req protocol.CreateInstanceRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.ErrorJSON(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}

	if req.Key.AssemblyID == "" {
		utils.ErrorJSON(w, http.StatusBadRequest, fmt.Errorf("key.assembly_id is required"))
		return
	}

	// A assembly body is optional: when supplied, ensure it is durable first
	// (idempotent), then lazily create the runtime from the registered
	// artifact. When omitted, the assembly must have been registered already.
	var domainKey protocol.InstanceKey
	if req.Assembly != nil {
		key, err := h.ensureRegistered(r, req)
		if err != nil {
			utils.ErrorJSON(w, http.StatusBadRequest, err)
			return
		}
		domainKey = key
	} else {
		domainKey = protocol.InstanceKey{
			AssemblyID: req.Key.AssemblyID,
			Version:  req.Key.Version,
			Hash:     req.Key.Hash,
			Env:      req.Key.Env,
		}
	}

	i, _, err := h.instances.GetOrCreate(r.Context(), domainKey)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		}
		utils.ErrorJSON(w, status, err)
		return
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "instance ready",
		Status:  http.StatusOK,
		Data: protocol.InstanceResponse{
			ID:       i.ID,
			Status:   string(i.Status()),
			AssemblyID: i.Key.AssemblyID,
			Version:  i.Key.Version,
			Hash:     i.Key.Hash,
			Env:      i.Key.Env,
		},
	})
}

// ensureRegistered persists the supplied assembly so instance creation can load
// it from the durable registry. It is idempotent: an already-registered key is
// left untouched. It returns the resolved identity under which the assembly is
// stored so callers address the same key.
func (h *Handler) ensureRegistered(r *http.Request, req protocol.CreateInstanceRequest) (protocol.InstanceKey, error) {
	ctx := r.Context()

	key := protocol.InstanceKey{
		AssemblyID: req.Key.AssemblyID,
		Version:  req.Key.Version,
		Hash:     req.Key.Hash,
		Env:      req.Key.Env,
	}
	if key.Version == "" {
		key.Version = "latest"
	}
	if key.Env == "" {
		key.Env = "development"
	}

	computed, err := protocol.HashAssembly(*req.Assembly)
	if err != nil {
		return protocol.InstanceKey{}, fmt.Errorf("hash assembly: %w", err)
	}
	if key.Hash != "" && key.Hash != computed {
		return protocol.InstanceKey{}, fmt.Errorf("key.hash does not match assembly content")
	}
	key.Hash = computed

	if _, err := h.compiler.Compile(*req.Assembly); err != nil {
		return protocol.InstanceKey{}, fmt.Errorf("invalid assembly: %w", err)
	}

	now := time.Now().UTC()
	_, _, err = h.assemblies.Register(ctx, assembly.RegisteredAssembly{
		Key:          key,
		Assembly:       *req.Assembly,
		RegisteredAt: now,
		UpdatedAt:    now,
	})
	return key, err
}
