#!/bin/sh

# This script installs universal-ctags within an alpine container.

# Commit hash of github.com/universal-ctags/ctags.
# Last bumped 2024-09-02.
CTAGS_VERSION=v6.1.0
CTAGS_ARCHIVE_TOP_LEVEL_DIR=ctags-6.1.0
# When using commits you can rely on
# CTAGS_ARCHIVE_TOP_LEVEL_DIR=ctags-$CTAGS_VERSION

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

apk --no-cache add \
  --virtual ctags-build-deps \
  autoconf \
  automake \
  binutils \
  curl \
  g++ \
  gcc \
  jansson-dev \
  make \
  pkgconfig

# ctags is dynamically linked against jansson
apk --no-cache add jansson

NUMCPUS=$(grep -c '^processor' /proc/cpuinfo)
CTAGS_TMPDIR=$(mktemp -d /tmp/ctags.XXXXXX)

# Installation
curl --retry 5 "https://codeload.github.com/universal-ctags/ctags/tar.gz/$CTAGS_VERSION" | tar xz -C "$CTAGS_TMPDIR"
cd "$CTAGS_TMPDIR/$CTAGS_ARCHIVE_TOP_LEVEL_DIR"
./autogen.sh
./configure --program-prefix=universal- --enable-json --disable-readcmd
make -j"$NUMCPUS" --load-average="$NUMCPUS"
make install
