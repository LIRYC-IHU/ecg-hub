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
# Pin ECG_BRIDGE_VERSION to a tag for reproducible builds (defaults to latest).
ARG ECG_BRIDGE_VERSION=latest
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

# The default FTP/DICOM/ECTP ports are all > 1024, so nothing here is needed at
# runtime. But some ECG devices (Nihon Kohden) can only send to FTP port 21,
# and a non-root process cannot bind a privileged port unless the *binary*
# carries the capability -- Docker's `cap_add` alone only widens the bounding
# set, it grants a non-root process nothing. Stamping it here is inert under
# the default `cap_drop: ALL`, which masks it away; it only takes effect when
# docker-compose.yml opts in. See the port-21 block there.
RUN apk add --no-cache libcap \
  && setcap cap_net_bind_service=+ep /app/ecg-hub \
  && apk del libcap

ENV LOG_LEVEL debug

USER app

EXPOSE 4444

ENTRYPOINT ["/app/ecg-hub"]
