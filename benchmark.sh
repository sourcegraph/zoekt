#!/usr/bin/env bash

cd "$(dirname "${BASH_SOURCE[0]}")"
set -euxo pipefail

OUTPUT=$(mktemp -d -t test)
cleanup() {
    rm -rf "$OUTPUT"
}
trap cleanup EXIT

export CTAGS_COMMAND="ctags"

REPO_URL="https://github.com/sgtest/megarepo.git"

go install ./cmd/... 

repoDir="${OUTPUT}/repo-checkout"
mkdir -p "${repoDir}"

indexDir="${OUTPUT}/index"
mkdir -p "${indexDir}"

git clone "${REPO_URL}" "${repoDir}"
echo "666" >"${repoDir}/SG_ID"

hyperfine --warmup 2 --prepare "rm -rf ${indexDir} && mkdir -p ${indexDir}" --parameter-scan cpu_percent 0.1 1.0 -D 0.1 "zoekt-sourcegraph-indexserver -index=${indexDir} -sourcegraph_url=${repoDir} -cpu_fraction={cpu_percent} -debug-index=666"
