#!/bin/bash

set -ex

cp -r repo repo17

go run ../cmd/zoekt-index -disable_ctags repo17
go run ../cmd/zoekt-merge-index repo17_v16.00000.zoekt
mv compound*zoekt repo17_v17.00000.zoekt

rm -rf repo17 repo17_v16.00000.zoekt zoekt-builder-shard-log.tsv

mv *.zoekt shards/
