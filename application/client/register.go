package client

import (
	"context"
	"net/http"

	"github.com/neuron-runtime/neuron/shared/types/protocol"
)

func (c *Client) Register(ctx context.Context, req protocol.RegisterRequest) (protocol.RegisterResponse, error) {

	var response struct {
		Data protocol.RegisterResponse `json:"data"`
	}

	if err := c.conn.Do(ctx, http.MethodPost, protocol.RegisterPath, req, &response); err != nil {
		return protocol.RegisterResponse{}, err
	}

	return response.Data, nil
}
