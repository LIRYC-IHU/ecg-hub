FROM golang:1.25-alpine AS build
WORKDIR /app

# Copy SDK first (layer cache)
COPY shared/ /app/shared/

# Copy module source
COPY philips/ /app/philips/
WORKDIR /app/philips
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /module-philips .

# --- Runtime ---
FROM alpine:3.21
RUN apk add --no-cache ca-certificates

# Install grpc_health_probe for Docker HEALTHCHECK
RUN wget -qO /bin/grpc_health_probe https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/v0.4.28/grpc_health_probe-linux-amd64 && \
    chmod +x /bin/grpc_health_probe

COPY --from=build /module-philips /module-philips

EXPOSE 50051
HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD ["/bin/grpc_health_probe", "-addr=:50051"]

ENTRYPOINT ["/module-philips"]
