#!/bin/bash

set -ex

# generate repo17.v17.0000.zoekt
cp -r repo repo17

go run ../cmd/zoekt-index -disable_ctags repo17
go run ../cmd/zoekt-merge-index merge repo17_v16.00000.zoekt
mv compound*zoekt repo17_v17.00000.zoekt

rm -rf repo17 repo17_v16.00000.zoekt zoekt-builder-shard-log.tsv

mv ./*.zoekt shards/

# generate repo2.v16.0000.zoekt
go run ../cmd/zoekt-index repo2
rm zoekt-builder-shard-log.tsv
mv ./*.zoekt shards/
