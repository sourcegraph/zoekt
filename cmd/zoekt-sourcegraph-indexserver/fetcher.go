package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/google/zoekt"
	"io"
	"os/exec"
	"strings"
)

// a deltaFetcher returns a list of changed files along with their file contents
//
// NOTE: The idea of returning the file contents in the same function seems awkward
// with local checkouts, but necessary in a remote one
type deltaFetcher interface {
	// Fetch returns the paths that have changed on the latest
	// commit for each branch, along with their file contents.
	Fetch(ctx context.Context, branches ...zoekt.RepositoryBranch) (branchDeltas, error)
}

type branchDeltas map[string]delta

// delta contains all the paths that have been added, changed, and removed
// along with the file contents
//
// TODO@ggilmore: should the file contents  be io.ReadClosers, and should delta offer a "close"
// method that closes all the file contents (thinking about the local file case)
type delta struct {
	// Added is a list of all the paths that have been added,
	// along with their file contents.
	Added map[string]io.Reader

	// Changed is a list of all the paths that have been changed,
	// along with their file contents.
	Changed map[string]io.Reader

	// Removed is a list of all the paths that were removed.
	Removed []string
}

// NOTE: We can mimic the existing behavior (albeit with extra work on Zoekt's end)
// by creating a fresh git checkout every time, feeding it to local fetcher,
// and copying all the files into a new repository for zoekt-git-index to process. This
// doesn't seem that useful, but we can do this to be able to merge a PR earlier.
//
// The delta will appear as if all files were deleted and then re-added on every commit.
//
// We can have another service whose job is just to manage a permanent local checkout (deal with corruption,
// garbage collection, evicting epos, etc.) but the implementation of localFetcher doesn't
// care about where this service comes from.

// localFetcher is a deltaFetcher that operates against a local git checkout
type localFetcher struct {
	// gitDir is the local file path to the .git folder for the repository
	gitDir string

	// remoteURL is the URL for the git remote that commits are fetched from
	remoteURL string

	// customRunCommand, if provided, is used to execute all constructed
	// git commands instead of just cmd.Run().
	customRunCommand func(*exec.Cmd) error
}

// Fetch returns the paths that have changed on the latest
// commit for each branch, along with their file contents.
func (lf *localFetcher) Fetch(ctx context.Context, branches ...zoekt.RepositoryBranch) (branchDeltas, error) {
	runCmd := func(cmd *exec.Cmd) error {
		return cmd.Run()
	}
	if lf.customRunCommand == nil {
		runCmd = lf.customRunCommand
	}

	gitCmd := func(args ...string) *exec.Cmd {
		args = append([]string{"git", "-C", lf.gitDir, "-c"}, args...)
		c := exec.CommandContext(ctx, "git", args...)
		c.Stdin = &bytes.Buffer{}
		return c
	}

	// Create branches in local checkout if they don't exist
	for _, b := range branches {
		// Does the local branch exist?
		ref := fmt.Sprintf("refs/heads/%s", b.Name)
		cmd := gitCmd("show-ref", "--verify", ref)

		var errB bytes.Buffer
		cmd.Stderr = &errB
		if err := runCmd(cmd); err != nil {
			if !strings.Contains(errB.String(), "not a valid ref") {
				return nil, fmt.Errorf("while checking to see if branch %q exists in local checkout: %w", b.Name, err)
			}

			// create the missing branch
			cmd := gitCmd(
				"checkout",
				"--orphan", // this branch will have no git history - which ensures that the delta will contain all changed files
				b.Name)
			cmd.Stdin = &bytes.Buffer{}
			if err := runCmd(cmd); err != nil {
				return nil, fmt.Errorf("while creating branch %q in local checkout: %w", b.Name, err)
			}

			// TODO@ggilmore: is it excessive to consider switching back to the starting branch before we started creating branches (leave the state as we left it)
		}
	}

	// TODO@ggilmore: I just copied this fetch logic. I feel like we should capture this in a helper function
	// or something, there is a lot of duplication here.

	// Shallow fetch each commit specified in zoekt.Branches. This requires
	// the server to have configured both uploadpack.allowAnySHA1InWant and
	// uploadpack.allowFilter. (See gitservice.go in the Sourcegraph repository)
	fetchArgs := []string{"protocol.version=2", "fetch", "--depth=1", lf.remoteURL}
	var commits []string
	for _, b := range branches {
		commits = append(commits, b.Version)
	}
	fetchArgs = append(fetchArgs, commits...)

	cmd := gitCmd(fetchArgs...)
	err := lf.customRunCommand(cmd)
	if err != nil {
		return nil, err
	}

	deltas := make(map[string]delta)

	for _, b := range branches {
		// local branch v commit we just fetched
		commitRange := fmt.Sprintf("%s..%s", b.Name, b.Version)

		// diff local branch against newly fetched commit
		cmd := gitCmd(
			"diff",
			"-z",            // separate filenames and statuses with NUL separates for more robust parsing
			"--name-status", // show only file names
			"--no-renames",  // show renamed files as a deletion followed by an addition
			commitRange,
		)

		var inB, outB bytes.Buffer
		cmd.Stdin = &inB
		cmd.Stdout = &outB
		err := lf.customRunCommand(cmd)
		if err != nil {
			return nil, fmt.Errorf("while diffing %q: %w", commitRange, err)
		}

		d, err := todoNoopParseDiff(outB)
		if err != nil {
			return nil, fmt.Errorf("failed to process parse diff for %q: %w", commitRange, err)
		}

		deltas[b.Name] = d
	}

	return deltas, nil
}

// TODO(@ggilmore): process the diff output to generate the delta
func todoNoopParseDiff(b bytes.Buffer) (delta, error) {
	return delta{}, nil
}
