
# Configuration parameters

Parameters are in the `zoekt` section of the git-config.

* `name`: name of the repository, typically HOST/PATH, eg. `github.com/hanwen/usb`.

* `web-url`: base URL for linking to files, commits, and the repository, eg.
`https://github.com/hanwen/usb`

* `web-url-type`: type of URL, eg. github. Supported are cgit,
  gitiles, gitweb, cgit and gitea.

* `github-stars`, `github-forks`, `github-watchers`,
  `github-subscribers`: counters for github interactions

## Examples

### gitea

Clone a remote repository and add the indexer configuration.

```sh
git clone --bare https://codeberg.org/Codeberg/gitea
cd gitea.git
git config zoekt.web-url-type gitea
git config zoekt.web-url https://codeberg.org/Codeberg/gitea
git config zoekt.name codeberg.org/Codeberg/gitea
```

The tail of the git *config* should then contain:

```ini
[zoekt]
	web-url-type = gitea
	web-url = https://codeberg.org/Codeberg/gitea
	name = codeberg.org/Codeberg/gitea
```

The *gitea.git* repository can then be indexed with `zoekt-git-index`

```sh
zoekt-git-index  --branches main  -index /data/index -repo_cache /data/repos gitea.git
```
