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

WORKDIR /app

COPY --from=builder /bin/ecg-hub /app/ecg-hub
COPY --from=builder /app/converter-fda/bin/* /usr/local/bin/

EXPOSE 4444

ENTRYPOINT ["/app/ecg-hub"]

