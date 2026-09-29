package health

import (
	"net/http"

	"github.com/neuron-runtime/neuron/nore/internal/api/utils"
	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func Health(w http.ResponseWriter, r *http.Request) {
	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "N.O.R.E. is healthy",
		Status:  http.StatusOK,
		Data:    map[string]string{"capability": "nore"},
	})
}
