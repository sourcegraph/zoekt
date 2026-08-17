#!/bin/sh

# Installs universal-ctags from prebuilt Linux tarballs (amd64/arm64).
# Zoekt looks up "universal-ctags" on PATH; the tarball installs "ctags".
#
# When bumping CTAGS_VERSION, update CTAGS_COMMIT and the sha256 sums below
# together from the release page/API:
# https://github.com/universal-ctags/ctags-nightly-build/releases

CTAGS_VERSION=2024.01.07
CTAGS_COMMIT=4053f69a35d8d3d307b274040f27c147eec79ee7
CTAGS_INSTALL_DIR=${CTAGS_INSTALL_DIR:-/usr/bin}
# GitHub release asset digests for uctags-${CTAGS_VERSION}-linux-*.tar.xz
CTAGS_TAR_SHA256_X86_64=0c0bbc9f81d3f7151988b94e78c64914ab41ee4c5e10debfe79f73fee54a68a0
CTAGS_TAR_SHA256_AARCH64=a50e25cb5b4ced8fea119984695e77464aaa73d5d4a53c10fec34dd82b1d9e5f

set -eux

if command -v apk >/dev/null 2>&1; then
  apk add --no-cache xz
fi

case "${TARGETARCH:-$(uname -m)}" in
  amd64 | x86_64)
    ctags_arch=x86_64
    tar_sha256=$CTAGS_TAR_SHA256_X86_64
    ;;
  arm64 | aarch64)
    ctags_arch=aarch64
    tar_sha256=$CTAGS_TAR_SHA256_AARCH64
    ;;
  *)
    echo "unsupported architecture: ${TARGETARCH:-$(uname -m)}" >&2
    exit 1
    ;;
esac

archive_name="uctags-${CTAGS_VERSION}-linux-${ctags_arch}.tar.xz"
extract_dir="uctags-${CTAGS_VERSION}-linux-${ctags_arch}"
base_url="https://github.com/universal-ctags/ctags-nightly-build/releases/download/${CTAGS_VERSION}%2B${CTAGS_COMMIT}"
archive_path="/tmp/${archive_name}"

wget -qO "$archive_path" "${base_url}/${archive_name}"

echo "$tar_sha256  $archive_path" | sha256sum -c -
tar -xJf "$archive_path" -C /tmp
install -m 0755 "/tmp/${extract_dir}/bin/ctags" "$CTAGS_INSTALL_DIR/ctags"
rm -rf "$archive_path" "/tmp/${extract_dir}"

ln -sf "$CTAGS_INSTALL_DIR/ctags" "$CTAGS_INSTALL_DIR/universal-ctags"
