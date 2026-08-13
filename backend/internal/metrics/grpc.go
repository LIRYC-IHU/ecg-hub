// gRPC/Connect RED metrics (requests_total, request_duration_seconds,
// in_flight) recorded by a Connect interceptor covering both unary and
// server-streaming RPCs. Mirrors the HTTP middleware in http.go.
package metrics

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	grpcRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "grpc_requests_total",
		Help: "Total number of gRPC/Connect RPCs handled, by service, method and Connect code.",
	}, []string{"service", "method", "code"})

	grpcRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "grpc_request_duration_seconds",
		Help:    "gRPC/Connect handler latency by service and method.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"service", "method"})

	grpcInFlight = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "grpc_requests_in_flight",
		Help: "Current number of gRPC/Connect RPCs in flight, by service.",
	}, []string{"service"})
)

func init() {
	Registry.MustRegister(grpcRequestsTotal, grpcRequestDuration, grpcInFlight)
}

// splitProcedure splits a Connect procedure like
// "/grpc.api.v1.AdminService/GetStats" into its short service name
// ("AdminService") and method ("GetStats"). The package prefix is dropped to
// keep the label set compact; the service/method pair is a bounded set so
// cardinality stays safe.
func splitProcedure(procedure string) (service, method string) {
	p := strings.TrimPrefix(procedure, "/")
	slash := strings.LastIndex(p, "/")
	if slash < 0 {
		return p, ""
	}
	service, method = p[:slash], p[slash+1:]
	if dot := strings.LastIndex(service, "."); dot >= 0 {
		service = service[dot+1:]
	}
	return service, method
}

// codeString maps an RPC result to its Connect code label ("ok" on success).
func codeString(err error) string {
	if err == nil {
		return "ok"
	}
	return connect.CodeOf(err).String()
}

type connectMetricsInterceptor struct{}

// ConnectMetricsInterceptor returns a Connect interceptor that instruments every
// unary and server-streaming RPC with grpc_requests_total /
// grpc_request_duration_seconds / grpc_requests_in_flight. Add it as the first
// (outermost) interceptor on each service so auth/validation failures are timed
// and counted with their Connect code too.
func ConnectMetricsInterceptor() connect.Interceptor { return connectMetricsInterceptor{} }

func (connectMetricsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		service, method := splitProcedure(req.Spec().Procedure)
		grpcInFlight.WithLabelValues(service).Inc()
		start := time.Now()
		resp, err := next(ctx, req)
		grpcInFlight.WithLabelValues(service).Dec()
		grpcRequestDuration.WithLabelValues(service, method).Observe(time.Since(start).Seconds())
		grpcRequestsTotal.WithLabelValues(service, method, codeString(err)).Inc()
		return resp, err
	}
}

// WrapStreamingClient is a pass-through — this interceptor is server-side only.
func (connectMetricsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (connectMetricsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		service, method := splitProcedure(conn.Spec().Procedure)
		grpcInFlight.WithLabelValues(service).Inc()
		start := time.Now()
		err := next(ctx, conn)
		grpcInFlight.WithLabelValues(service).Dec()
		grpcRequestDuration.WithLabelValues(service, method).Observe(time.Since(start).Seconds())
		grpcRequestsTotal.WithLabelValues(service, method, codeString(err)).Inc()
		return err
	}
}
