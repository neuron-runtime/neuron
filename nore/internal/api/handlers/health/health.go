package health

import (
	"net/http"

	"github.com/Muhammad-Jay/neuron/nore/internal/api/utils"
	"github.com/Muhammad-Jay/neuron/shared/types/protocol"
)

func Health(w http.ResponseWriter, r *http.Request) {
	utils.WriteJSON(w, http.StatusOK, protocol.Response{
		Message: "N.O.R.E. is healthy",
		Status:  http.StatusOK,
		Data:    map[string]string{"capability": "nore"},
	})
}
