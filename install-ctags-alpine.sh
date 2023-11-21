#!/bin/sh
set -eux
# shellcheck disable=SC3040
set -o pipefail || true
# Commit from 2023-10-24. Please always pick a commit from the main branch.
export SOURCEGRAPH_COMMIT=45a6748bb491513b9e1162d888711ca9b3bb4303
wget -O - https://raw.githubusercontent.com/sourcegraph/sourcegraph/$SOURCEGRAPH_COMMIT/cmd/symbols/ctags-install-alpine.sh | sh
