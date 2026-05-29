package shared

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	modulepb "github.com/LIRYC-IHU/ecg-hub-module-sdk/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

// ModuleServer is the bootstrap for a gRPC module service.
// It handles listener creation, signal handling, health registration,
// and graceful shutdown.
type ModuleServer struct {
	Name    string
	Port    int
	Handler modulepb.ModuleServiceServer
}

// Run starts the gRPC server and blocks until SIGINT/SIGTERM.
func (s *ModuleServer) Run() error {
	port := s.Port
	if port == 0 {
		port = 50051
	}
	if envPort := os.Getenv("MODULE_PORT"); envPort != "" {
		fmt.Sscanf(envPort, "%d", &port)
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	grpcServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			Time:              30 * time.Second,
			Timeout:           10 * time.Second,
		}),
		grpc.MaxRecvMsgSize(16*1024*1024),
		grpc.MaxSendMsgSize(16*1024*1024),
		grpc.ChainUnaryInterceptor(
			RecoveryInterceptor(),
			LoggingInterceptor(),
		),
	)

	modulepb.RegisterModuleServiceServer(grpcServer, s.Handler)

	healthServer := health.NewServer()
	healthServer.SetServingStatus(s.Name, healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	reflection.Register(grpcServer)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		slog.Info("module: shutting down", "signal", sig.String(), "name", s.Name)
		healthServer.SetServingStatus(s.Name, healthpb.HealthCheckResponse_NOT_SERVING)
		grpcServer.GracefulStop()
	}()

	slog.Info("module: gRPC server started", "name", s.Name, "port", port)
	return grpcServer.Serve(lis)
}
