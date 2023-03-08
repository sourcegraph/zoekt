#!/usr/bin/env bash

cd "$(dirname "${BASH_SOURCE[0]}")/../.."
set -euo pipefail

find . -name "buf.gen.yaml" -not -path ".git" | while read -r buf_yaml; do
  pushd "$(dirname "${buf_yaml}")" >/dev/null

  if ! buf generate; then
    echo "running buf generate on ${buf_yaml} failed, please examine the output and fix the issues"
    exit 1
  fi

  popd >/dev/null
done

if ! git diff --exit-code; then
  echo "buf generate produced changes in the above file(s), please run buf generate and commit the changes"
  exit 1
fi

if ! (git ls-files --others --exclude-standard . | tee >(grep -q .)); then
  echo "buf generate produced the above untracked file(s), please run buf generate and commit them"
  exit 1
fi
