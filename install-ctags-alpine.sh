#!/bin/sh
set -x
# Commit from 2022-03-01. Please always pick a commit from the main branch.
export SOURCEGRAPH_COMMIT=20497508d57afd4bbd35597629779255d772a7f8
wget -O - https://raw.githubusercontent.com/sourcegraph/sourcegraph/$SOURCEGRAPH_COMMIT/cmd/symbols/ctags-install-alpine.sh | sh
