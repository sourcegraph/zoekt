#!/usr/bin/env bash

#

cd "$(dirname "${BASH_SOURCE[0]}")"
set -euxo pipefail

rm -rf sys || true

./ttar.sh -C "$(pwd)" -x -f ./sys.ttar
