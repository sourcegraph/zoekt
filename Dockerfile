# syntax=docker/dockerfile:1.7
FROM golang:1.26.2-alpine AS builder

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
    go build \
      -trimpath \
      -ldflags "-X github.com/sourcegraph/zoekt.Version=$VERSION" \
      -o /out/ \
      ./cmd/...

FROM alpine:3

RUN apk add --no-cache git ca-certificates bind-tools tini jansson wget

COPY --chmod=755 install-ctags-alpine.sh /usr/local/bin/install-ctags-alpine.sh
RUN /usr/local/bin/install-ctags-alpine.sh && \
    rm /usr/local/bin/install-ctags-alpine.sh \
      /usr/local/bin/universal-optscript

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
