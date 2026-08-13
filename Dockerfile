# syntax=docker/dockerfile:1.7
# Build on the native host arch and cross-compile; avoid QEMU for `go build`.
FROM --platform=$BUILDPLATFORM golang:1.26.2-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache ca-certificates

ENV CGO_ENABLED=0
WORKDIR /src

# Cache dependency resolution separately from source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out && \
    GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build \
    -trimpath \
    -ldflags "-X github.com/sourcegraph/zoekt.Version=$VERSION" \
    -o /out/ \
    ./cmd/...

FROM alpine:3

RUN apk add --no-cache git ca-certificates bind-tools tini

COPY --chmod=755 install-ctags-alpine.sh /usr/local/bin/install-ctags-alpine.sh
ARG TARGETARCH
RUN TARGETARCH="$TARGETARCH" /usr/local/bin/install-ctags-alpine.sh && \
    rm /usr/local/bin/install-ctags-alpine.sh

RUN addgroup -S zoekt && \
    adduser -S -G zoekt -h /home/zoekt zoekt && \
    mkdir -p /data/index /home/zoekt && \
    chown -R zoekt:zoekt /data /home/zoekt

COPY --from=builder /out/ /usr/local/bin/

USER zoekt
WORKDIR /home/zoekt

ENV DATA_DIR=/data/index

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["zoekt-webserver", "-index", "/data/index", "-pprof", "-rpc"]
