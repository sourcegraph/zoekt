# Zoekt: fast code search

    "Zoekt, en gij zult spinazie eten" - Jan Eertink

    ("seek, and ye shall eat spinach" - My primary school teacher)

Zoekt is a text search engine intended for use with source
code. (Pronunciation: roughly as you would pronounce "zooked" in English)

**Note:** This has been the maintained source for Zoekt since 2017, when it was forked from the
original repository [github.com/google/zoekt](https://github.com/google/zoekt).

## Background

Zoekt supports fast substring and regexp matching on source code, with a rich query language
that includes boolean operators (and, or, not). It can search individual repositories, and search
across many repositories in a large codebase. Zoekt ranks search results using a combination of code-related signals
like whether the match is on a symbol. Because of its general design based on trigram indexing and syntactic
parsing, it works well for a variety of programming languages.

The two main ways to use the project are
* Through individual commands, to index repositories and perform searches through Zoekt's [query language](doc/query_syntax.md)
* Or, through the indexserver and webserver, which support syncing repositories from a code host and searching them through a web UI or API

For more details on Zoekt's design, see the [docs directory](doc/).

## Usage

### Installation

Install the commands you need with `go install`, as shown below. The examples
use the latest released version and assume that Go's binary installation
directory is on your `PATH`.

**Note**: It is also recommended to install [Universal ctags](https://github.com/universal-ctags/ctags), as symbol
information is a key signal in ranking search results. See [ctags.md](doc/ctags.md) for more information.

### Command-based usage

Zoekt supports indexing and searching repositories on the command line. This is most helpful
for simple local usage, or for testing and development.

#### Indexing a local git repo

    go install github.com/sourcegraph/zoekt/cmd/zoekt-git-index@latest
    zoekt-git-index -index ~/.zoekt /path/to/repo

#### Synchronizing local git repos

`zoekt-local-sync` recursively discovers Git repositories under one or more
roots. Repository names are relative to their root; the command fails before
changing the index if two repositories have the same name. By default the
command previews the repositories that would be removed and indexed; pass `-f`
to apply the sync. The supplied roots are the complete desired set for the
index directory, so repositories not found beneath them are removed. Use
separate index directories for repository sets that should be managed
independently:

    go install github.com/sourcegraph/zoekt/cmd/zoekt-local-sync@latest
    go install github.com/sourcegraph/zoekt/cmd/zoekt-git-index@latest
    zoekt-local-sync -index ~/.zoekt ~/src
    zoekt-local-sync -index ~/.zoekt -f ~/src

The `list` and `remove` subcommands inspect and explicitly remove repositories.
Removal, like synchronization, previews by default and requires `-f` to apply:

    zoekt-local-sync list -index ~/.zoekt
    zoekt-local-sync remove -index ~/.zoekt path/to/repo
    zoekt-local-sync remove -index ~/.zoekt -f path/to/repo

#### Indexing a local directory (not git-specific)

    go install github.com/sourcegraph/zoekt/cmd/zoekt-index@latest
    zoekt-index -index ~/.zoekt /path/to/repo

#### Searching an index

    go install github.com/sourcegraph/zoekt/cmd/zoekt@latest
    zoekt 'hello'
    zoekt 'hello file:README'

### Zoekt services

Zoekt also contains an index server and web server to support larger-scale indexing and searching
of remote repositories. The index server can be configured to periodically fetch and reindex repositories
from a code host. The webserver can be configured to serve search results through a web UI or API.

#### Indexing a GitHub organization

    go install github.com/sourcegraph/zoekt/cmd/zoekt-indexserver@latest
    go install github.com/sourcegraph/zoekt/cmd/zoekt-mirror-github@latest
    go install github.com/sourcegraph/zoekt/cmd/zoekt-git-index@latest

    echo YOUR_GITHUB_TOKEN_HERE > token.txt
    echo '[{"GitHubOrg": "apache", "CredentialPath": "token.txt"}]' > config.json

    zoekt-indexserver -mirror_config config.json -data_dir ~/.zoekt/

This will fetch all repos under 'github.com/apache', then index the repositories. The indexserver takes care of
periodically fetching and indexing new data, and cleaning up logfiles. See [config.go](cmd/zoekt-indexserver/config.go)
for more details on this configuration.

#### Starting the web server

    go install github.com/sourcegraph/zoekt/cmd/zoekt-webserver@latest
    zoekt-webserver -index ~/.zoekt/

This will start a web server with a simple search UI at http://localhost:6070.
See the [query syntax docs](doc/query_syntax.md) for more details on the query
language.

#### Container image

Zoekt publishes a single container image at `ghcr.io/sourcegraph/zoekt`. It
includes the Zoekt binaries, `git`, and `universal-ctags`. By default it runs
`zoekt-webserver` against `/data/index`:

    docker run --rm -p 6070:6070 -v "$PWD/index:/data/index" ghcr.io/sourcegraph/zoekt

You can override the default command to run `zoekt-indexserver` instead. This
example stores cloned repositories, logs, and indexes in the named Docker
volume `zoekt-data` and reads a mounted mirror config file:

    docker run --rm \
      -v "$PWD/config.json:/config.json:ro" \
      -v "$PWD/token.txt:/home/zoekt/token.txt:ro" \
      -v zoekt-data:/data \
      ghcr.io/sourcegraph/zoekt \
      zoekt-indexserver -mirror_config /config.json -data_dir /data

If you start the web server with `-rpc`, it exposes a [simple JSON search
API](doc/json-api.md) at `http://localhost:6070/api/search`.

The JSON API supports advanced features including:
- Alternative BM25 scoring (using the `UseBM25Scoring` option)
- Context lines around matches (using the `NumContextLines` option)

Finally, the web server exposes a [gRPC API](grpc/protos/zoekt/webserver/v1/webserver.proto)
that supports structured query objects, streaming search results, and advanced
search options.

## Acknowledgements

Thanks to Han-Wen Nienhuys for creating Zoekt. Thanks to Alexander Neubeck for
coming up with this idea, and helping Han-Wen Nienhuys flesh it out.
