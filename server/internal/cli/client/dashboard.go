package client

import "context"

// HealthResponse is the response shape for GET /health. DB/HA/EngineReady
// are booleans on the wire (server/internal/api/health.go's healthResponse)
// — a string here would make every real response fail to json.Unmarshal.
type HealthResponse struct {
	Status      string `json:"status"`
	DB          bool   `json:"db"`
	HA          bool   `json:"ha"`
	EngineReady bool   `json:"engine_ready"`
	Version     string `json:"version"`
}

// Health returns GET /health — this node's own status, including its own
// HA role. It is what `gdhcp ha status` reports, and does not require
// authentication (docs/api/endpoints.md).
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var h HealthResponse
	if err := c.get(ctx, "/api/v1/health", nil, &h); err != nil {
		return nil, err
	}
	return &h, nil
}
