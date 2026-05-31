# Stage 1: Build
FROM golang:1.26.2-alpine AS builder

RUN apk add --no-cache git ca-certificates make

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/ecg-hub ./cmd/ecg-hub

WORKDIR /app/converter-fda
RUN make re

# Stage 2: Runtime
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

# Non-root runtime user (NFR-S1). Fixed UID/GID so mounted-volume ownership is predictable.
RUN addgroup -g 10001 app && adduser -D -u 10001 -G app app

WORKDIR /app

COPY --from=builder /bin/ecg-hub /app/ecg-hub
COPY --from=builder /app/converter-fda/bin/* /usr/local/bin/

# Pre-create the storage dirs owned by the app user so named volumes inherit
# writable ownership on first creation. FTP/DICOM listen on non-privileged
# ports (>1024), so no root capabilities are required at runtime.
RUN mkdir -p /data/ecg /data/ecg-quarantine && chown -R app:app /data /app

USER app

EXPOSE 4444

ENTRYPOINT ["/app/ecg-hub"]

