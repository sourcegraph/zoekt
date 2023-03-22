# API

When running `zoekt-webserver` with the `-rpc` option there will be a JSON HTTP API available for searches at `/api/search`:

```
curl -XPOST -d '{"Q":"needle"}' 'http://127.0.0.1:6070/api/search'
```

## Filtering by repository IDs

If your projects are indexed with a `repoid` (added automatically by some
indexers) then you can filter your searches to a subset of repositories
efficiently using the `RepoIDs` filter:

```
curl -XPOST -d '{"Q":"needle","RepoIDs":[1234,4567]}' 'http://34.120.239.98/api/search'
```

## Options

There are multiple options that can be passed under `Opts` which can also be
found at
[SearchOptions](https://github.com/xvandish/zoekt/blob/58cf4748830ac0eded1517cc8c2454694c531fbd/api.go#L470).

```
curl -XPOST -d '{"Q":"needle","Opts":{"EstimateDocCount":true,"NumContextLines":10}}' 'http://34.120.239.98/api/search'
```
