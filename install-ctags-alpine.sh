#!/bin/sh

# Installs universal-ctags from prebuilt Alpine APKs (amd64/arm64).
# Zoekt looks up "universal-ctags" on PATH; the APK installs "ctags".
#
# sha256 sums below are GitHub release asset digests. When bumping
# CTAGS_VERSION or CTAGS_COMMIT, copy them from the release page/API:
# https://github.com/universal-ctags/ctags-nightly-build/releases

CTAGS_VERSION=${CTAGS_VERSION:-2026.08.11}
CTAGS_COMMIT=${CTAGS_COMMIT:-8361949f6a2465fb1bbaf26a234278c3c3cbd3ac}
# GitHub release asset digests for uctags-${CTAGS_VERSION}-linux-*.release.apk
CTAGS_APK_SHA256_X86_64=bde53a3092fd540e004bf7923322a21604592734aea002086764f02456378c9d
CTAGS_APK_SHA256_AARCH64=245239f8097a2877ad91d14e742670d25ecd5bac7d2b77fdb0139dfcce291d22

set -eux

case "${TARGETARCH:-$(uname -m)}" in
  amd64 | x86_64)
    apk_name="uctags-${CTAGS_VERSION}-linux-x86_64.release.apk"
    apk_sha256=$CTAGS_APK_SHA256_X86_64
    ;;
  arm64 | aarch64)
    apk_name="uctags-${CTAGS_VERSION}-linux-aarch64.release.apk"
    apk_sha256=$CTAGS_APK_SHA256_AARCH64
    ;;
  *) echo "unsupported architecture: ${TARGETARCH:-$(uname -m)}" >&2; exit 1 ;;
esac

base_url="https://github.com/universal-ctags/ctags-nightly-build/releases/download/${CTAGS_VERSION}%2B${CTAGS_COMMIT}"
apk_path="/tmp/${apk_name}"

wget -qO "$apk_path" "${base_url}/${apk_name}"
wget -qO /etc/apk/keys/uctags.rsa.pub "${base_url}/${apk_name}.rsa.pub"

echo "$apk_sha256  $apk_path" | sha256sum -c -
apk add --no-cache "$apk_path"
rm "$apk_path"

ln -sf /usr/bin/ctags /usr/bin/universal-ctags
