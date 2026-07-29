# API

When running `zoekt-webserver` with the `-rpc` option there will be a JSON HTTP API available for searches at `/api/search`:

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"Q":"needle"}' \
  http://127.0.0.1:6070/api/search
```

## Filtering by repository IDs

If your projects are indexed with a `repoid` (added automatically by some
indexers) then you can filter your searches to a subset of repositories
efficiently using the `RepoIDs` filter:

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"Q":"needle","RepoIDs":[1234,4567]}' \
  http://127.0.0.1:6070/api/search
```

## Listing repositories

Use `/api/list` to return metadata for repositories that match a query:

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"Q":"repo:sourcegraph"}' \
  http://127.0.0.1:6070/api/list
```

## Options

There are multiple options that can be passed under `Opts`; see the
[SearchOptions](../api.go) type for the current fields.

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"Q":"needle","Opts":{"EstimateDocCount":true,"NumContextLines":10}}' \
  http://127.0.0.1:6070/api/search
```
