package instances

import (
	"fmt"
	"net/http"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func (h *Handler) GetInstanceByID(w http.ResponseWriter, r *http.Request) {
	id := utils.PathID(r.PathValue("id"))
	i, ok := h.resolveInstance(r, id)
	if !ok {
		utils.ErrorJSON(w, http.StatusNotFound, fmt.Errorf("instance %s not found", id))
		return
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "instance",
		Status:  http.StatusOK,
		Data: protocol.InstanceResponse{
			ID:         i.ID,
			Status:     string(i.Status()),
			AssemblyID: i.Key.AssemblyID,
			Version:    i.Key.Version,
			Hash:       i.Key.Hash,
			Env:        i.Key.Env,
		},
	})
}
