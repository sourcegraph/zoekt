#!/usr/bin/env bash

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
set -euxo pipefail

find . -name "buf.gen.yaml" -not -path ".git" | while read -r buf_yaml; do
  echo "running buf generate on ${buf_yaml}"
  pushd "$(dirname "${buf_yaml}")" >/dev/null

  buf generate

  popd >/dev/null
done

if ! git diff --exit-code; then
  echo "buf generate produced changes in the above file(s), please run buf generate and commit the changes"
  exit 1
fi

if git ls-files --others --exclude-standard . | tee >(grep -q .); then
  echo "buf generate produced untracked files in the above file(s), please run buf generate and commit the changes"
  exit 1
fi
