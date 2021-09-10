FROM golang:alpine AS builder

RUN apk add --no-cache ca-certificates

ENV CGO_ENABLED=0 GO111MODULE=on
WORKDIR /go/src/github.com/google/zoekt

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
ARG VERSION
RUN go install -ldflags "-X github.com/google/zoekt.Version=$VERSION" ./cmd/...

FROM alpine:3.11 AS ctags

RUN apk add --no-cache --virtual build-deps ca-certificates curl jansson-dev \
    libseccomp-dev linux-headers autoconf pkgconfig make automake \
    gcc g++ binutils

ENV CTAGS_VERSION=681a8d5f5f6fcca9d8cca703c250f4cdf05b45c3

RUN curl -fsSL -o ctags.tar.gz "https://codeload.github.com/universal-ctags/ctags/tar.gz/$CTAGS_VERSION" && \
    tar -C /tmp -xzf ctags.tar.gz && cd /tmp/ctags-$CTAGS_VERSION && \
    ./autogen.sh && LDFLAGS=-static ./configure --program-prefix=universal- --enable-json --enable-seccomp && \
    make -j8 && make install && cd && \
    rm -rf /tmp/ctags-$CTAGS_VERSION && \
    apk --no-cache --purge del build-deps

FROM alpine AS zoekt

RUN apk update --no-cache && apk upgrade --no-cache && \
    apk add --no-cache git ca-certificates bind-tools tini

COPY --from=ctags /usr/local/bin/universal-* /usr/local/bin/
COPY --from=builder /go/bin/* /usr/local/bin/

# zoekt-webserver has a large stable heap size (10s of gigs), and as such the
# default GOGC=100 could be better tuned. https://dave.cheney.net/tag/gogc
ENV GOGC=50

ENTRYPOINT ["/sbin/tini", "--"]
