# ECG Hub Module SDK

Go library for building ECG Hub vendor modules as independent gRPC microservices.

## Quick Start

```go
package main

import (
    "context"
    "log"

    "github.com/LIRYC-IHU/ecg-hub-module-sdk"
    pb "github.com/LIRYC-IHU/ecg-hub-module-sdk/proto"
)

type myModule struct {
    pb.UnimplementedModuleServiceServer
    health *shared.HealthHelper
}

func (m *myModule) GetCapabilities(_ context.Context, _ *pb.GetCapabilitiesRequest) (*pb.GetCapabilitiesResponse, error) {
    return &pb.GetCapabilitiesResponse{
        Name:               "my-vendor",
        AcceptedExtensions: []string{".xyz"},
        Version:            "1.0.0",
        Description:        "My custom ECG format parser",
    }, nil
}

func (m *myModule) Validate(_ context.Context, req *pb.ValidateRequest) (*pb.ValidateResponse, error) {
    // Check if file matches your format
    valid := len(req.Data) > 4 && string(req.Data[:4]) == "MYEC"
    return &pb.ValidateResponse{Valid: valid}, nil
}

func (m *myModule) Parse(_ context.Context, req *pb.ParseRequest) (*pb.ParseResponse, error) {
    // Extract metadata from file bytes
    return &pb.ParseResponse{
        Metadata: &pb.ECGMetadata{
            PatientId:  "extracted-id",
            VendorName: "my-vendor",
        },
    }, nil
}

func (m *myModule) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
    return m.health.Serving(ctx, req)
}

func main() {
    handler := &myModule{health: shared.NewHealthHelper()}
    server := &shared.ModuleServer{
        Name:    "my-vendor",
        Port:    50051,
        Handler: handler,
    }
    if err := server.Run(); err != nil {
        log.Fatal(err)
    }
}
```

## Dockerfile

```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /module .

FROM alpine:3.19
COPY --from=build /module /module
EXPOSE 50051
HEALTHCHECK --interval=10s --timeout=3s \
  CMD ["/bin/grpc_health_probe", "-addr=:50051"]
ENTRYPOINT ["/module"]
```

## Docker Compose

```yaml
module-my-vendor:
  build: ./modules/my-vendor
  volumes:
    - ecg-data:/data/ecg
  expose:
    - "50051"
  healthcheck:
    test: ["CMD", "/bin/grpc_health_probe", "-addr=:50051"]
    interval: 10s
    timeout: 3s
    retries: 3
```

## Hub Configuration

```yaml
modules:
  remote:
    - name: my-vendor
      address: module-my-vendor:50051
```

## SDK Features

- **`ModuleServer`** — gRPC server bootstrap with graceful shutdown, signal handling, health registration
- **`HealthHelper`** — Default Health RPC with uptime tracking
- **`LoggingInterceptor`** — Logs method, duration, status code for every RPC
- **`RecoveryInterceptor`** — Catches panics, logs stack trace, returns Internal error

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MODULE_PORT` | `50051` | gRPC listen port |

## Proto Contract

The full contract is defined in `proto/ecghub/module/v1/module.proto`. Key RPCs:

| RPC | Purpose | Timeout |
|-----|---------|---------|
| `GetCapabilities` | Module identity (name, extensions, formats) | 5s |
| `Validate` | Quick format check | 5s |
| `Parse` | Full metadata extraction | 30s |
| `UpdateFile` | Write metadata back to file on volume | 10s |
| `RenamePatientID` | Rewrite patient ID in file bytes | 10s |
| `Health` | Operational status | 2s |
