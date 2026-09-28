package instances

import (
	"net/http"

	"github.com/Muhammad-Jay/neuron/nore/internal/api/utils"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

func (h *Handler) ListInstances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items := make([]protocol.InstanceResponse, 0)

	all := utils.ParseBool(q, "all", false)
	status := q.Get("status")

	for _, i := range h.instances.List(protocol.ListOptions{Status: status, All: all}) {
		items = append(items, protocol.InstanceResponse{
			ID:                i.ID,
			Status:            string(i.Status()),
			AssemblyID:          i.Key.AssemblyID,
			BlueprintMetadata: i.Blueprint.Metadata,
			Version:           i.Key.Version,
			Hash:              i.Key.Hash,
			Env:               i.Key.Env,
		})
	}

	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "instances",
		Status:  http.StatusOK,
		Data:    items,
	})
}
