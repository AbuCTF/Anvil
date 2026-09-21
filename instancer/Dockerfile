# syntax=docker/dockerfile:1.7
# Anvil instancer operator. Build context is the instancer/ module.
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o instancer ./cmd

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /app/instancer .
USER 65532:65532
ENTRYPOINT ["/instancer"]
