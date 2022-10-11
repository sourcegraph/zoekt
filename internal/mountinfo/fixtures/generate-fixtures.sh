#!/usr/bin/env bash

# Regenerates the contents of the fixtures/sys directory

cd "$(dirname "${BASH_SOURCE[0]}")"
set -euxo pipefail

rm -rf sys || true

./ttar.sh -v -C "$(pwd)" -x -f ./sys.ttar
