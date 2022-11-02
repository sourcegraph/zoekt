#!/bin/sh
set -eux
set -o pipefail
# Commit from 2022-04-05. Please always pick a commit from the main branch.
export SOURCEGRAPH_COMMIT=4dd4ce3d91da5cac2ac6169d3005714247178f57
wget -O - https://raw.githubusercontent.com/sourcegraph/sourcegraph/$SOURCEGRAPH_COMMIT/cmd/symbols/ctags-install-alpine.sh | sh
