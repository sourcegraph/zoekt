FROM golang:1.20.3-alpine3.17 AS builder

RUN apk add --no-cache ca-certificates

ENV CGO_ENABLED=0 GO111MODULE=on
WORKDIR /go/src/github.com/sourcegraph/zoekt

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
ARG VERSION
RUN go install -ldflags "-X github.com/sourcegraph/zoekt.Version=$VERSION" ./cmd/...

FROM rust:alpine3.17 AS rust-builder

RUN git clone --depth=1 --branch main https://github.com/sourcegraph/sourcegraph
RUN cargo install --path sourcegraph/docker-images/syntax-highlighter --root /syntect_server --bin scip-ctags

FROM alpine:3.17.3 AS zoekt

RUN apk update --no-cache && apk upgrade --no-cache && \
    apk add --no-cache git ca-certificates bind-tools tini jansson wget

COPY install-ctags-alpine.sh .
RUN ./install-ctags-alpine.sh && rm install-ctags-alpine.sh

COPY --from=builder /go/bin/* /usr/local/bin/
COPY --from=rust-builder /syntect_server/bin/scip-ctags /usr/local/bin/scip-ctags

ENTRYPOINT ["/sbin/tini", "--"]
