package instances

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/nore/internal/instance"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func (h *Handler) RemoveInstance(w http.ResponseWriter, r *http.Request) {
	force, err := forceRequested(r)
	if err != nil {
		utils.ErrorJSON(w, http.StatusBadRequest, err)
		return
	}
	id := utils.PathID(r.PathValue("id"))
	removed, err := h.instances.Remove(r.Context(), id, force)
	if err != nil {
		writeInstanceRemovalError(w, err)
		return
	}
	if !removed {
		utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("instance %s not found", id))
		return
	}
	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "instance removed",
		Status:  http.StatusOK,
	})
}

func forceRequested(r *http.Request) (bool, error) {
	raw := r.URL.Query().Get("force")
	if raw == "" {
		return false, nil
	}
	force, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid force value %q: %w", raw, err)
	}
	return force, nil
}

func writeInstanceRemovalError(w http.ResponseWriter, err error) {
	if errors.Is(err, instance.ErrActiveExecutions) {
		utils.ErrorJSON(w, http.StatusConflict, err)
		return
	}
	utils.ErrorJSON(w, http.StatusInternalServerError, err)
}
