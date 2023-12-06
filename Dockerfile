FROM golang:1.21.4-alpine3.18 AS builder

RUN apk add --no-cache ca-certificates

ENV CGO_ENABLED=0
WORKDIR /go/src/github.com/sourcegraph/zoekt

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
ARG VERSION
RUN go install -ldflags "-X github.com/sourcegraph/zoekt.Version=$VERSION" ./cmd/...

FROM rust:alpine3.18 AS rust-builder

RUN apk update --no-cache && apk upgrade --no-cache && \
    apk add --no-cache git wget musl-dev>=1.1.24-r10 build-base

RUN wget -qO- https://github.com/sourcegraph/sourcegraph/archive/0c8aa18eece45922a2b56dc0f94e21b1bb533e7d.tar.gz | tar xz && mv sourcegraph-* sourcegraph

ARG TARGETARCH

# Because .cargo/config.toml doesnt support triplet-specific env
RUN cd sourcegraph/docker-images/syntax-highlighter && /sourcegraph/cmd/symbols/cargo-config.sh && cd /

RUN cargo install --path sourcegraph/docker-images/syntax-highlighter --root /syntect_server --bin scip-ctags

FROM alpine:3.18 AS zoekt

RUN apk update --no-cache && apk upgrade --no-cache && \
    apk add --no-cache git ca-certificates bind-tools tini jansson wget

COPY install-ctags-alpine.sh .
RUN ./install-ctags-alpine.sh && rm install-ctags-alpine.sh

COPY --from=builder /go/bin/* /usr/local/bin/
COPY --from=rust-builder /syntect_server/bin/scip-ctags /usr/local/bin/scip-ctags

ENTRYPOINT ["/sbin/tini", "--"]
