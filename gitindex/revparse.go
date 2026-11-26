package gitindex

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// resolveCommit resolves ref to a commit with git rev-parse in gitDir.
//
// With a prefix, ref names a branch in that namespace: <prefix>/<ref> wins over
// any other ref or object ID of the same name. Otherwise, such as for HEAD, a
// tag or an object ID, ref resolves as git resolves it.
func resolveCommit(gitDir, prefix, ref string) (plumbing.Hash, error) {
	if prefix != "" {
		name := path.Join(prefix, ref)
		// git would also expand the shorthand heads/x to refs/heads/heads/x.
		if kind, _, _ := strings.Cut(name, "/"); kind == "heads" || kind == "tags" || kind == "remotes" {
			name = "refs/" + name
		}
		id, err := revParseCommit(gitDir, name)
		if !errors.Is(err, plumbing.ErrReferenceNotFound) {
			return id, err
		}
	}
	return revParseCommit(gitDir, ref)
}

func revParseCommit(gitDir, rev string) (plumbing.Hash, error) {
	// Without --end-of-options, which needs git 2.30, a leading dash would be an
	// option. No ref name starts with one.
	if strings.HasPrefix(rev, "-") {
		return plumbing.ZeroHash, plumbing.ErrReferenceNotFound
	}
	cmd := gitCommand(gitDir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		// Callers that allow missing branches check for this error.
		return plumbing.ZeroHash, plumbing.ErrReferenceNotFound
	}
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git rev-parse %q: %w: %s", rev, err, bytes.TrimSpace(stderr.Bytes()))
	}
	hex := strings.TrimSpace(string(out))
	if !plumbing.IsHash(hex) {
		return plumbing.ZeroHash, fmt.Errorf("git rev-parse %q: unexpected output %q", rev, out)
	}
	return plumbing.NewHash(hex), nil
}

// gitCommand returns a git command that runs in the git directory gitDir.
// GIT_DIR=. keeps git from using an inherited GIT_DIR or searching parent
// directories for another repository, as Sourcegraph's gitserver does. Like
// go-git, git never fetches missing objects from a promisor remote here.
func gitCommand(gitDir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = gitDir
	cmd.Env = append(os.Environ(), "GIT_DIR=.", "GIT_NO_LAZY_FETCH=1")
	return cmd
}

// repoGitDir returns the git directory of a repository stored on disk.
func repoGitDir(repo *git.Repository) (string, bool) {
	s, ok := repo.Storer.(interface{ Filesystem() billy.Filesystem })
	if !ok {
		return "", false
	}
	return s.Filesystem().Root(), true
}

// gitBinaryDisabled reports whether ZOEKT_DISABLE_CATFILE_BATCH=true asks
// for go-git instead of the git binary, as a kill switch.
func gitBinaryDisabled() bool {
	disabled, _ := strconv.ParseBool(os.Getenv("ZOEKT_DISABLE_CATFILE_BATCH"))
	return disabled
}
