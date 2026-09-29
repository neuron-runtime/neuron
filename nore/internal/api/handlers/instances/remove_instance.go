package instances

import (
	"fmt"
	"net/http"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func (h *Handler) RemoveInstance(w http.ResponseWriter, r *http.Request) {
	id := utils.PathID(r.PathValue("id"))
	removed, err := h.instances.Remove(r.Context(), id)
	if err != nil {
		utils.ErrorJSON(w, http.StatusInternalServerError, err)
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
