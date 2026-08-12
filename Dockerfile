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

RUN apk add --no-cache git ca-certificates bind-tools tini wget

# Prebuilt universal-ctags from nightly releases (amd64/arm64).
# Zoekt looks up "universal-ctags" on PATH; the APK installs "ctags".
ARG TARGETARCH
ARG CTAGS_VERSION=2026.08.11
ARG CTAGS_COMMIT=8361949f6a2465fb1bbaf26a234278c3c3cbd3ac
RUN set -eux; \
    case "$TARGETARCH" in \
    amd64) ctags_arch=x86_64 ;; \
    arm64) ctags_arch=aarch64 ;; \
    *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    apk_name="uctags-${CTAGS_VERSION}-linux-${ctags_arch}.release.apk"; \
    base_url="https://github.com/universal-ctags/ctags-nightly-build/releases/download/${CTAGS_VERSION}%2B${CTAGS_COMMIT}"; \
    wget -O "/tmp/${apk_name}" "${base_url}/${apk_name}"; \
    wget -O /etc/apk/keys/uctags.rsa.pub "${base_url}/${apk_name}.rsa.pub"; \
    apk add --no-cache "/tmp/${apk_name}"; \
    rm "/tmp/${apk_name}"; \
    ln -s ctags /usr/bin/universal-ctags

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
