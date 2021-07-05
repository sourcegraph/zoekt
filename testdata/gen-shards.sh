#!/bin/bash

set -ex

go build ../cmd/zoekt-index

cp -r repo repo17

./zoekt-index -disable_ctags repo17

rm -rf repo17

mv *.zoekt shards/
