FROM golang:1.25-alpine AS build
WORKDIR /app

# Copy SDK first (layer cache)
COPY shared/ /app/shared/

# Copy module source
COPY nihon-kohden/ /app/nihon-kohden/
WORKDIR /app/nihon-kohden
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /module-nk .

# --- Runtime ---
FROM alpine:3.21
RUN apk add --no-cache ca-certificates

# Install grpc_health_probe for Docker HEALTHCHECK
RUN wget -qO /bin/grpc_health_probe https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/v0.4.28/grpc_health_probe-linux-amd64 && \
    chmod +x /bin/grpc_health_probe

# nk-to-fda converter binary — must be copied in at build time or mounted via volume
# COPY --from=converter /nk-to-fda /usr/local/bin/nk-to-fda
# For dev: mount from host or set BRIDGE_NK_TO_FDA env var

COPY --from=build /module-nk /module-nk

EXPOSE 50051
EXPOSE 30003

HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD ["/bin/grpc_health_probe", "-addr=:50051"]

ENTRYPOINT ["/module-nk"]
