# Stage 1: Build static binary
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates
RUN apk add --no-cache make

WORKDIR /app

COPY go.mod go.sum ./
COPY ./converter-fda ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/ecg-hub ./cmd/ecg-hub

# Install ecg-convertor tools

WORKDIR /app/converter-fda

RUN make

# Install ecg-bridge conversion tools (GOPATH=/go in golang:alpine image → binaries land in /go/bin/).
# Add more RUN lines here as new tools are published in github.com/LIRYC-IHU/ecg-bridge.
# RUN go install github.com/LIRYC-IHU/ecg-bridge/cmd/philips-to-fda@latest
# RUN go install github.com/LIRYC-IHU/ecg-bridge/cmd/muse-to-fda@latest
# RUN go install github.com/LIRYC-IHU/ecg-bridge/cmd/mfer-to-fda@latest

# Stage 2: Minimal runtime image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bin/ecg-hub /app/ecg-hub
# Copy ecg-bridge binaries from /go/bin (golang:alpine GOPATH=/go).
COPY --from=builder /app/converter-fda/bin/* /usr/local/bin/.
# COPY --from=builder /go/bin/muse-to-fda /usr/local/bin/muse-to-fda

EXPOSE 4444

ENTRYPOINT ["/app/ecg-hub"]

