FROM golang:1.17.6-alpine3.15 AS builder

RUN apk add --no-cache ca-certificates

ENV CGO_ENABLED=0 GO111MODULE=on
WORKDIR /go/src/github.com/google/zoekt

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
ARG VERSION
RUN go install -ldflags "-X github.com/google/zoekt.Version=$VERSION" ./cmd/...

FROM alpine:3.15.0 AS zoekt

RUN apk update --no-cache && apk upgrade --no-cache && \
    apk add --no-cache git ca-certificates bind-tools tini jansson

# Commit from 2022-02-10. Please always pick a commit from the main branch.
ENV SOURCEGRAPH_COMMIT=d9f1ea582042a98a5ab1e6f0e9147ad0ca996df8
ADD https://raw.githubusercontent.com/sourcegraph/sourcegraph/$SOURCEGRAPH_COMMIT/cmd/symbols/ctags-install-alpine.sh /tmp/
RUN sh /tmp/ctags-install-alpine.sh && rm /tmp/ctags-install-alpine.sh

COPY --from=builder /go/bin/* /usr/local/bin/

# zoekt-webserver has a large stable heap size (10s of gigs), and as such the
# default GOGC=100 could be better tuned. https://dave.cheney.net/tag/gogc
ENV GOGC=50

ENTRYPOINT ["/sbin/tini", "--"]
