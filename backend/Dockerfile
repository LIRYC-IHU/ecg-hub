# Stage 1: Build
FROM golang:1.26.2-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/ecg-hub ./cmd/ecg-hub

# Conversion tools are pulled straight from the public ecg-bridge repo via
# `go install` instead of being built from the local converter-fda tree.
# Pinned so a tagged release can be rebuilt byte-for-byte later. Bump this
# deliberately; `latest` would make the image depend on when it was built.
ARG ECG_BRIDGE_VERSION=v1.1.2
ENV GOBIN=/converters
RUN set -eux; \
  for tool in \
  fda-to-dicom fda-to-pdf \
  dicom-to-fda \
  mindray-to-dicom mindray-to-fda mindray-to-pdf \
  muse-to-dicom muse-to-fda muse-to-pdf \
  nk-to-dicom nk-to-fda nk-to-pdf \
  philips-to-dicom philips-to-fda philips-to-pdf; do \
  go install -ldflags="-s -w" "github.com/LIRYC-IHU/ecg-bridge/cmd/${tool}@${ECG_BRIDGE_VERSION}"; \
  done

# Stage 2: Runtime
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

# Non-root runtime user (NFR-S1). Fixed UID/GID so mounted-volume ownership is predictable.
RUN addgroup -g 10001 app && adduser -D -u 10001 -G app app

WORKDIR /app

COPY --from=builder /bin/ecg-hub /app/ecg-hub
COPY --from=builder /converters/* /usr/local/bin/

# Pre-create the storage dirs owned by the app user so named volumes inherit
# writable ownership on first creation.
RUN mkdir -p /data/ecg /data/ecg-quarantine && chown -R app:app /data /app

# No setcap here, deliberately. A binary carrying file capabilities cannot be
# exec'd at all when the permitted set they ask for is unavailable -- which is
# exactly what `cap_drop: ALL` and `no-new-privileges` produce -- and the
# kernel reports it as "exec /app/ecg-hub: operation not permitted", not as a
# bind error. Nothing needs it either: docker-compose publishes the device
# ports, so dockerd does the privileged bind and the app binds >1024 inside.

ENV LOG_LEVEL debug

USER app

EXPOSE 4444

ENTRYPOINT ["/app/ecg-hub"]
