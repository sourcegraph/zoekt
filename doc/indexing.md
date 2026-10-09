
# Configuration parameters

Parameters are in the `zoekt` section of the git-config.

* `name`: name of the repository, typically HOST/PATH, eg. `github.com/hanwen/usb`.

* `web-url`: base URL for linking to files, commits, and the repository, eg.
`https://github.com/hanwen/usb`

* `web-url-type`: code host used to generate links. Supported values are
  `azuredevops`, `bitbucket-cloud`, `bitbucket-server`, `cgit`, `gitea`,
  `github`, `gitiles`, `gitlab`, `gitweb`, and `source.bazel.build`.

* `github-stars`, `github-forks`, `github-watchers`,
  `github-subscribers`: counters for github interactions

## Examples

### gitea

Clone a remote repository and add the indexer configuration.

```sh
git clone --bare https://codeberg.org/Codeberg/Community
cd Community.git
git config zoekt.web-url-type gitea
git config zoekt.web-url https://codeberg.org/Codeberg/Community
git config zoekt.name codeberg.org/Codeberg/Community
```

The tail of the git *config* should then contain:

```ini
[zoekt]
	web-url-type = gitea
	web-url = https://codeberg.org/Codeberg/Community
	name = codeberg.org/Codeberg/Community
```

The *Community.git* repository can then be indexed with `zoekt-git-index`

```sh
zoekt-git-index -branches main -index /data/index -repo_cache /data/repos Community.git
```

# Cat-file and partial clones

`zoekt-git-index` can read blobs through `git cat-file` instead of go-git.
This optimization is disabled by default. Set `ZOEKT_DISABLE_CATFILE_BATCH=false`
to opt in, or `true` to switch back to go-git.

In a partial clone, Git can fetch missing objects from a promisor remote on
demand. Even `cat-file --filter=blob:limit=...` can fetch a missing blob to learn
its size before excluding it from the output. The resulting `git index-pack`
subprocess can use memory proportional to the uncompressed blob size, even
though Zoekt skips the file.

With Git 2.45 or newer, set `GIT_NO_LAZY_FETCH=1` when running `zoekt-git-index`
to index only locally available content. Fetch all content you want indexed
first, including any large-file exceptions. Disabling lazy fetching on an
arbitrary partial clone can otherwise omit wanted content from search results.

Without lazy fetching, cat-file reports absent blobs as `missing`, not
`excluded`, because their sizes are unknown locally. Both Zoekt blob readers
assume missing blobs were omitted by a size filter and preserve the filename with
the explanation `NOT-INDEXED: exceeds the maximum size limit`. This keeps the
user-visible reason consistent when switching readers; it is not a size check,
so objects missing for other reasons receive the same explanation. The cat-file
reader also logs one count of missing files per repository indexing pass.
