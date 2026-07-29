
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
