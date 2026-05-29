package shared

import (
	"context"
	"time"

	modulepb "github.com/LIRYC-IHU/ecg-hub-module-sdk/proto"
)

// HealthHelper provides a default Health RPC implementation that tracks uptime.
type HealthHelper struct {
	startTime time.Time
}

// NewHealthHelper creates a HealthHelper with the current time as start.
func NewHealthHelper() *HealthHelper {
	return &HealthHelper{startTime: time.Now()}
}

// Serving returns a HealthResponse indicating the module is serving.
func (h *HealthHelper) Serving(_ context.Context, _ *modulepb.HealthRequest) (*modulepb.HealthResponse, error) {
	return &modulepb.HealthResponse{
		Status:        modulepb.HealthResponse_STATUS_SERVING,
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
	}, nil
}
