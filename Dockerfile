FROM golang:1.25.0-alpine3.22 AS builder

RUN apk add --no-cache ca-certificates

ENV CGO_ENABLED=0
WORKDIR /go/src/github.com/sourcegraph/zoekt

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
ARG VERSION=dev
RUN go install -ldflags "-X github.com/sourcegraph/zoekt.Version=$VERSION" ./cmd/...

FROM alpine:3.22

RUN apk add --no-cache git ca-certificates bind-tools tini jansson wget

COPY install-ctags-alpine.sh .
RUN ./install-ctags-alpine.sh && rm install-ctags-alpine.sh

RUN addgroup -S zoekt && \
    adduser -S -G zoekt -h /home/zoekt zoekt && \
    mkdir -p /data/index /home/zoekt && \
    chown -R zoekt:zoekt /data /home/zoekt

COPY --from=builder /go/bin/* /usr/local/bin/

USER zoekt
WORKDIR /home/zoekt

ENV DATA_DIR=/data/index

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["zoekt-webserver", "-index", "/data/index", "-pprof", "-rpc"]
