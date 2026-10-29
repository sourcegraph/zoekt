#!/bin/sh

# Builds universal-ctags from its release tarball and installs a static binary
# in an Alpine image. Zoekt looks up "universal-ctags" on PATH.
#
# Keep CTAGS_VERSION in sync with the universal-ctags Sourcegraph ships
# (wolfi-packages/ctags.yaml in sourcegraph/sourcegraph), which is also built
# with JSON as the only optional feature. When bumping CTAGS_VERSION, update
# CTAGS_TAR_SHA256 from the release page/API:
# https://github.com/universal-ctags/ctags/releases
#
# ctag-overlay.nix reads the CTAGS_* lines below to give the Nix flake (CI,
# orbs, nix develop) the same ctags, so keep them as unquoted NAME=value lines.

CTAGS_VERSION=6.2.1
CTAGS_INSTALL_DIR=${CTAGS_INSTALL_DIR:-/usr/bin}
# GitHub release asset digest for universal-ctags-${CTAGS_VERSION}.tar.gz
CTAGS_TAR_SHA256=2c63efe9e0e083dc50e6fdd8c5414781cc8873d8c8940cf553c01870ed962f8c

CTAGS_TMPDIR=

cleanup() {
  apk --no-cache --purge del ctags-build-deps || true
  cd /
  if [ -n "$CTAGS_TMPDIR" ]; then
    rm -rf "$CTAGS_TMPDIR"
  fi
}

trap cleanup EXIT

set -eux

apk --no-cache add --virtual ctags-build-deps \
  build-base \
  jansson-dev \
  jansson-static \
  pkgconf

CTAGS_TMPDIR=$(mktemp -d /tmp/ctags.XXXXXX)
archive_path="$CTAGS_TMPDIR/universal-ctags-${CTAGS_VERSION}.tar.gz"

wget -qO "$archive_path" "https://github.com/universal-ctags/ctags/releases/download/v${CTAGS_VERSION}/universal-ctags-${CTAGS_VERSION}.tar.gz"
echo "$CTAGS_TAR_SHA256  $archive_path" | sha256sum -c -
tar -xzf "$archive_path" -C "$CTAGS_TMPDIR"

cd "$CTAGS_TMPDIR/universal-ctags-${CTAGS_VERSION}"
./configure --enable-static --enable-json \
  --disable-xml --disable-yaml --disable-pcre2 --disable-seccomp
make -j"$(nproc)"
install -s -m 0755 ctags "$CTAGS_INSTALL_DIR/ctags"

ln -sf "$CTAGS_INSTALL_DIR/ctags" "$CTAGS_INSTALL_DIR/universal-ctags"
