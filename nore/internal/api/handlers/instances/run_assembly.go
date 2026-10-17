package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/nore/internal/assembly"
	"github.com/neuron-runtime/neuron/nore/internal/storage"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

// ListAssemblies returns the Assemblies registered with this runtime.
func (h *Handler) ListAssemblies(w http.ResponseWriter, r *http.Request) {
	all, err := h.assemblies.List(r.Context())
	if err != nil {
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]protocol.AssemblyResponse, 0, len(all))
	for _, a := range all {
		out = append(out, assemblyResponse(a.Key, a.RegisteredAt))
	}
	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "assemblies",
		Status:  http.StatusOK,
		Data:    out,
	})
}

// GetAssembly returns metadata for one registered Assembly by name. The latest
// version is resolved when the name carries no version.
func (h *Handler) GetAssembly(w http.ResponseWriter, r *http.Request) {
	name := utils.PathID(r.PathValue("name"))
	if name == "" {
		utils.ErrorJSON(w, http.StatusBadRequest, fmt.Errorf("missing assembly name"))
		return
	}
	reg, err := h.resolveAssembly(r.Context(), name)
	if err != nil {
		utils.ErrorJSON(w, assemblyErrorStatus(err), err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "assembly",
		Status:  http.StatusOK,
		Data:    assemblyResponse(reg.Key, reg.RegisteredAt),
	})
}

// RunNamed executes a named registered Assembly and returns its result.
func (h *Handler) RunNamed(w http.ResponseWriter, r *http.Request) {
	name := utils.PathID(r.PathValue("name"))
	if name == "" {
		utils.ErrorJSON(w, http.StatusBadRequest, fmt.Errorf("missing assembly name"))
		return
	}
	reg, err := h.resolveAssembly(r.Context(), name)
	if err != nil {
		utils.ErrorJSON(w, assemblyErrorStatus(err), err)
		return
	}
	h.runKey(w, r, reg.Key)
}

// RunDefault executes the default registered Assembly and returns its result.
// A clear error is returned if no default can be determined from registrations.
func (h *Handler) RunDefault(w http.ResponseWriter, r *http.Request) {
	key, err := h.resolveDefaultKey(r.Context())
	if err != nil {
		utils.ErrorJSON(w, http.StatusBadRequest, err)
		return
	}
	h.runKey(w, r, key)
}

// runKey starts a synchronous execution of a resolved Assembly key and writes
// the response. It is the named-assembly counterpart to Run, reporting the same
// result contract through the shared runSynchronously implementation.
func (h *Handler) runKey(w http.ResponseWriter, r *http.Request, key protocol.InstanceKey) {
	i, err := h.runningInstanceByKey(r.Context(), key)
	if err != nil {
		utils.ErrorJSON(w, runErrorStatus(err), err)
		return
	}

	var body protocol.RunRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	result, err := h.runSynchronously(r.Context(), i, body.Params)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return // the client went away; nothing can be written
		}
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
		return
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "execution completed",
		Status:  http.StatusOK,
		Data:    result,
	})
}

// resolveAssembly returns the latest registered artifact for an Assembly name.
func (h *Handler) resolveAssembly(ctx context.Context, name string) (assembly.RegisteredAssembly, error) {
	reg, err := h.assemblies.Resolve(ctx, protocol.InstanceKey{
		AssemblyID: name,
		Version:    protocol.VersionLatest,
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return assembly.RegisteredAssembly{}, fmt.Errorf("registered assembly %q not found: %w", name, err)
		}
		return assembly.RegisteredAssembly{}, err
	}
	return reg, nil
}

// resolveDefaultKey chooses the Assembly a name-less request runs. Registrations
// carry no default marker, so a single registered Assembly is the default and
// several require an explicit name.
func (h *Handler) resolveDefaultKey(ctx context.Context) (protocol.InstanceKey, error) {
	all, err := h.assemblies.List(ctx)
	if err != nil {
		return protocol.InstanceKey{}, err
	}
	if len(all) == 0 {
		return protocol.InstanceKey{}, fmt.Errorf("no registered assemblies")
	}
	if len(all) == 1 {
		return all[0].Key, nil
	}
	names := make([]string, 0, len(all))
	for _, a := range all {
		names = append(names, a.Key.AssemblyID)
	}
	return protocol.InstanceKey{}, fmt.Errorf(
		"multiple assemblies registered (%s); specify an assembly name", strings.Join(names, ", "))
}

func assemblyResponse(key protocol.InstanceKey, registeredAt time.Time) protocol.AssemblyResponse {
	return protocol.AssemblyResponse{
		AssemblyID:   key.AssemblyID,
		Version:      key.Version,
		Hash:         key.Hash,
		Env:          key.Env,
		RegisteredAt: registeredAt,
	}
}

// assemblyErrorStatus maps an Assembly resolution failure to an HTTP status. A
// name that addresses no registered Assembly is not found; anything else is an
// unexpected server-side failure.
func assemblyErrorStatus(err error) int {
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
