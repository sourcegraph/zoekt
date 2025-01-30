
    "Zoekt, en gij zult spinazie eten" - Jan Eertink

    ("seek, and ye shall eat spinach" - My primary school teacher)

Zoekt is a text search engine intended for use with source
code. (Pronunciation: roughly as you would pronounce "zooked" in English)

**Note:** This is a [Sourcegraph](https://github.com/sourcegraph/zoekt) fork
of the original repository [github.com/google/zoekt](https://github.com/google/zoekt).
It is now the maintained source for Zoekt.

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

# Usage

## Installation

    go get github.com/sourcegraph/zoekt/

**Note**: It is also recommended to install [Universal ctags](https://github.com/universal-ctags/ctags), as symbol
information is a key signal in ranking search results. See [ctags.md](doc/ctags.md) for more information.

## Command-based usage

Zoekt supports indexing and searching repositories on the command line. This is most helpful
for simple local usage, or for testing and development.

### Indexing a local git repo

    go install github.com/sourcegraph/zoekt/cmd/zoekt-git-index
    $GOPATH/bin/zoekt-git-index -index ~/.zoekt /path/to/repo

### Indexing a local directory (not git-specific)

    go install github.com/sourcegraph/zoekt/cmd/zoekt-index
    $GOPATH/bin/zoekt-index -index ~/.zoekt /path/to/repo

### Searching an index

    go install github.com/sourcegraph/zoekt/cmd/zoekt
    $GOPATH/bin/zoekt 'hello'
    $GOPATH/bin/zoekt 'hello file:README'

## Zoekt services

Zoekt also contains an index server and web server to support larger-scale indexing and searching
of remote repositories. The index server can be configured to periodically fetch and reindex repositories
from a code host. The webserver can be configured to serve search results through a web UI or API.

### Indexing a GitHub organization
    
    go install github.com/sourcegraph/zoekt/cmd/zoekt-indexserver

    echo YOUR_GITHUB_TOKEN_HERE > token.txt
    echo '[{"GitHubOrg": "apache", "CredentialPath": "token.txt"}]' > config.json

    $GOPATH/bin/zoekt-indexserver -mirror_config config.json -data_dir ~/.zoekt/ 

This will fetch all repos under 'github.com/apache', then index the repositories. The indexserver takes care of
periodically fetching and indexing new data, and cleaning up logfiles. See [config.go](cmd/zoekt-indexserver/config.go)
for more details on this configuration.

### Starting the web server

    go install github.com/sourcegraph/zoekt/cmd/zoekt-webserver
    $GOPATH/bin/zoekt-webserver -index ~/.zoekt/

This will start a web server with a simple search UI at http://localhost:6070. See the [uuery syntax docs](doc/query_syntax.md)
for more details on the query language.

If you start the web server with `-rpc`, it exposes a [simple JSON search API](doc/json-api.md) at `http://localhost:6070/search/api/search.

Finally, the web server exposes a gRPC API that supports [structured query objects](query/query.go) and advanced search options.

# Acknowledgements

Thanks to Han-Wen Nienhuys for creating Zoekt. Thanks to Alexander Neubeck for
coming up with this idea, and helping Han-Wen Nienhuys flesh it out.
