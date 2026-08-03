# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/envpilot-agent ./apps/agent

FROM alpine:3.21

RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 10001 envpilot && \
    adduser -S -D -H -u 10001 -G envpilot -h /var/lib/envpilot-agent envpilot && \
    mkdir -p /var/lib/envpilot-agent && \
    chown -R envpilot:envpilot /var/lib/envpilot-agent
COPY --from=builder /out/envpilot-agent /usr/local/bin/envpilot-agent
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/envpilot-agent"]
