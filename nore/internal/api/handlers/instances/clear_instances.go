package instances

import (
	"net/http"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func (h *Handler) ClearInstances(w http.ResponseWriter, r *http.Request) {
	force, err := forceRequested(r)
	if err != nil {
		utils.ErrorJSON(w, http.StatusBadRequest, err)
		return
	}
	if err := h.instances.Clear(r.Context(), force); err != nil {
		writeInstanceRemovalError(w, err)
		return
	}
	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "instances cleared",
		Status:  http.StatusOK,
	})
}
