#!/bin/sh

# Installs universal-ctags from prebuilt Alpine APKs (amd64/arm64).
# Zoekt looks up "universal-ctags" on PATH; the APK installs "ctags".

CTAGS_VERSION=${CTAGS_VERSION:-2026.08.11}
CTAGS_COMMIT=${CTAGS_COMMIT:-8361949f6a2465fb1bbaf26a234278c3c3cbd3ac}

set -eux

case "${TARGETARCH:-$(uname -m)}" in
  amd64 | x86_64) ctags_arch=x86_64 ;;
  arm64 | aarch64) ctags_arch=aarch64 ;;
  *) echo "unsupported architecture: ${TARGETARCH:-$(uname -m)}" >&2; exit 1 ;;
esac

apk_name="uctags-${CTAGS_VERSION}-linux-${ctags_arch}.release.apk"
base_url="https://github.com/universal-ctags/ctags-nightly-build/releases/download/${CTAGS_VERSION}%2B${CTAGS_COMMIT}"

wget -qO "/tmp/${apk_name}" "${base_url}/${apk_name}"
wget -qO /etc/apk/keys/uctags.rsa.pub "${base_url}/${apk_name}.rsa.pub"
apk add --no-cache "/tmp/${apk_name}"
rm "/tmp/${apk_name}"

ln -sf /usr/bin/ctags /usr/bin/universal-ctags
