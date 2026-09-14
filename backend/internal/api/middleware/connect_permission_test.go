package middleware

import (
	"context"
	"testing"

	"connectrpc.com/connect"
)

// allowAll grants every permission, so the only thing that can refuse a call in
// these tests is the mapping itself.
type allowAll struct{}

func (allowAll) HasPermission(context.Context, string, string) bool { return true }

// A procedure absent from the permission map must be refused, not served.
//
// This used to be a comma-ok that skipped the check when the procedure was
// missing, so a new RPC whose entry nobody added was reachable by every
// authenticated caller. The caller here holds every permission, which is what
// makes the refusal attributable to the missing mapping and nothing else.
func TestConnectRequirePermissionRefusesAnUnmappedProcedure(t *testing.T) {
	served := false
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		served = true
		return nil, nil
	})

	interceptor := ConnectRequirePermission(allowAll{}, map[string]string{
		"/grpc.api.v1.DeviceService/ListDevices": "device.read",
	})

	handler := interceptor(next)
	req := connect.NewRequest(&struct{}{})

	_, err := handler(context.Background(), &specRequest{AnyRequest: req, procedure: "/grpc.api.v1.DeviceService/CreateDevice"})
	if err == nil {
		t.Fatal("an unmapped procedure was served")
	}
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Errorf("code = %v, want Internal", got)
	}
	if served {
		t.Error("the handler ran for a procedure with no permission mapping")
	}
}

// The mapped procedure still reaches the handler, so the refusal above is the
// missing entry and not the interceptor refusing everything.
func TestConnectRequirePermissionServesAMappedProcedure(t *testing.T) {
	served := false
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		served = true
		return nil, nil
	})

	interceptor := ConnectRequirePermission(allowAll{}, map[string]string{
		"/grpc.api.v1.DeviceService/ListDevices": "device.read",
	})

	req := connect.NewRequest(&struct{}{})
	if _, err := interceptor(next)(context.Background(),
		&specRequest{AnyRequest: req, procedure: "/grpc.api.v1.DeviceService/ListDevices"}); err != nil {
		t.Fatalf("a mapped procedure was refused: %v", err)
	}
	if !served {
		t.Error("the handler did not run for a mapped procedure")
	}
}

// specRequest overrides Spec() so a procedure can be named without building a
// real Connect client and server for it.
type specRequest struct {
	connect.AnyRequest
	procedure string
}

func (r *specRequest) Spec() connect.Spec {
	return connect.Spec{Procedure: r.procedure, StreamType: connect.StreamTypeUnary}
}
