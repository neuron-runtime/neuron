package register

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req protocol.RegisterRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		utils.ErrorJSON(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}

	key, err := resolveKey(req)
	if err != nil {
		utils.ErrorJSON(w, http.StatusBadRequest, err)
		return
	}

	if _, err := h.compiler.Compile(req.Assembly); err != nil {
		utils.ErrorJSON(w, http.StatusBadRequest, fmt.Errorf("invalid assembly: %w", err))
		return
	}

	now := time.Now().UTC()
	reg := assembly.RegisteredAssembly{
		Key:                     key,
		Assembly:                req.Assembly,
		ExecutionConfigurations: req.ExecutionConfigurations,
		RegisteredAt:            now,
		UpdatedAt:               now,
	}

	if req.Force {
		// Clear the assembly for (name, version) and remove any instances built
		// from it so the replacement is authoritative.
		if _, err := h.instances.RemoveByAssembly(r.Context(), key); err != nil {
			utils.ErrorJSON(w, http.StatusInternalServerError, err)
			return
		}
		if err := h.assemblies.Delete(r.Context(), protocol.InstanceKey{AssemblyID: key.AssemblyID, Version: key.Version}); err != nil {
			utils.ErrorJSON(w, http.StatusInternalServerError, err)
			return
		}
	}

	created, replaced, err := h.assemblies.Register(r.Context(), reg)
	if err != nil {
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
		return
	}

	status := protocol.RegisterStatusRegistered
	message := "assembly registered"
	switch {
	case replaced:
		status = protocol.RegisterStatusReplaced
		message = "assembly replaced"
	case !created:
		status = protocol.RegisterStatusAlreadyRegistered
		message = "assembly already registered"
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: message,
		Status:  http.StatusOK,
		Data: protocol.RegisterResponse{
			Key:     key,
			Status:  status,
			Message: message,
		},
	})
}

// resolveKey derives the durable identity for a registration: the client key
// is honored when provided (Hash is validated against the content), otherwise
// the key is derived from the assembly metadata and content hash.
func resolveKey(req protocol.RegisterRequest) (protocol.InstanceKey, error) {
	if req.Key.AssemblyID != "" {
		if req.Key.Hash != "" {
			computed, err := protocol.HashAssembly(req.Assembly)
			if err != nil {
				return protocol.InstanceKey{}, fmt.Errorf("hash assembly: %w", err)
			}
			if computed != req.Key.Hash {
				return protocol.InstanceKey{}, fmt.Errorf("key.hash does not match assembly content")
			}
		}
		if req.Key.Version == "" {
			req.Key.Version = "latest"
		}
		if req.Key.Env == "" {
			req.Key.Env = "development"
		}
		return req.Key, nil
	}

	key, err := protocol.AssemblyKey(req.Assembly, req.Key.Env)
	if err != nil {
		return protocol.InstanceKey{}, err
	}
	return key, nil
}
