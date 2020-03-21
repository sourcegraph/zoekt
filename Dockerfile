FROM golang:alpine AS builder

RUN apk add --no-cache ca-certificates

ENV CGO_ENABLED=0 GO111MODULE=on
WORKDIR /go/src/github.com/google/zoekt

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN go install ./cmd/...

FROM alpine AS ctags

RUN apk add --no-cache --virtual build-deps ca-certificates curl jansson-dev \
    libseccomp-dev linux-headers autoconf pkgconfig make automake \
    gcc g++ binutils

ENV CTAGS_VERSION=1a94658c2cb47304d0e9811d49e0a5f48bdb3a0a

RUN curl -fsSL -o ctags.tar.gz "https://codeload.github.com/universal-ctags/ctags/tar.gz/$CTAGS_VERSION" && \
    tar -C /tmp -xzf ctags.tar.gz && cd /tmp/ctags-$CTAGS_VERSION && \
    ./autogen.sh && LDFLAGS=-static ./configure --program-prefix=universal- --enable-json --enable-seccomp && \
    make -j8 && make install && cd && \
    rm -rf /tmp/ctags-$CTAGS_VERSION && \
    apk --no-cache --purge del build-deps

FROM alpine AS zoekt

RUN apk add --no-cache git ca-certificates bind-tools tini

COPY .ctags.d /.ctags.d
COPY --from=ctags /usr/local/bin/universal-* /usr/local/bin/
COPY --from=builder /go/bin/* /usr/local/bin/

# zoekt-webserver has a large stable heap size (10s of gigs), and as such the
# default GOGC=100 could be better tuned. https://dave.cheney.net/tag/gogc
ENV GOGC=50

ENTRYPOINT ["/sbin/tini", "--"]
