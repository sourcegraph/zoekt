package main

import (
	"fmt"
	"io"
	"os"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

type fileChange struct {
	// path is the path of the file relative to the repository's root directory
	path string

	// contents, if provided, returns the contents for the new version of the file.
	// A nil "contents" represents a deleted file
	contents func() ([]byte, error)
}

type getChangesArguments struct {
	// from and to are the commits that are being compared (e.g. from...to)
	// TODO: if from isn't specified, we should provide an oracle using the existing shards
	from, to string

	// repoName is the name of the repository
	repoName string

	// remoteURL is the URL of the repository's remote
	remoteURL string
}

// getChanges determines the set of changes files from the given arguments.
//
// After callers have finished processing the returned list of changes, they should invoke the "cleanup"
// callback in order to release the resources that were needed to compute the diff.
func getChanges(args *getChangesArguments) (changes []fileChange, cleanup func() error, err error) {
// 	d, err := tmpGitDir(args.repoName)
// 	if err != nil {
// 		return nil, nil, fmt.Errorf("creating folder for git repository: %w", err)
// 	}
// 	cleanup = func() error {
// 		return os.RemoveAll(d)
// 	}

// 	// initialize a bare git repository
// 	repo, err := git.PlainInit(d, true)
// 	if err != nil {
// 		return nil, nil, fmt.Errorf("initializing git repository: %w", err)
// 	}

// 	// we need to change the Ref that HEAD is pointing to

// 	var branches []string

// 	// read all existing branches in the repo
// 	iter, err := repo.Branches()
// 	if err != nil {
// 		fmt.Errorf("reading repostitory branches: %w", err)
// 	}

// 	for {
// 		r, err := iter.Next()
// 		if err == io.EOF {
// 			break
// 		}

// 		if err != nil {
// 			fmt.Errorf("iterating over repository branches: %w", err)
// 		}

// 		branches = append(branches, r.Name().Short())
// 	}

// 	// TODO: Handle default branch similar to old code path - we'll need to add the non-existent branch (+ make sure HEAD is pointing to it), then delete the old branches
// 	// we have to do this since we can't set any config options before we clone the repo
// 	// TODO: is it worth using go-git here? Maybe we can just clone it with plain git exec and then Open() it ?
// 	// TODO: What are we gaining from this library? avoiding the need to parse the diff?

// 	// create remote to fetch from
// 	remoteName := "sourcegraph-gitserver"
// 	err = repo.DeleteRemote(remoteName)
// 	if err != nil && err != git.ErrRemoteNotFound {
// 		return nil, nil, fmt.Errorf("deleting old %q remote: %w", remoteName, err)
// 	}

// 	_, err = repo.CreateRemote(&config.RemoteConfig{
// 		Name: remoteName,
// 		URLs: []string{args.remoteURL},
// 	})
// 	if err != nil {
// 		return nil, nil, fmt.Errorf("creating %q remote: %w", remoteName, err)
// 	}

// 	// fetch objects for requested "from" and "to" commits
// 	err = repo.Fetch(&git.FetchOptions{
// 		RefSpecs:   []config.RefSpec{config.RefSpec(args.from), config.RefSpec(args.to)},
// 		Depth:      1,
// 		RemoteName: remoteName,
// 	})
// 	if err != nil {
// 		return nil, nil, fmt.Errorf("fetching references: %w", err)
// 	}

	// TODO : There isn't a lot of benefit to using go-git's fetch implementation above over native git's here
	//. We're also considering lifting the following diff logic to be processes by zoekt-git-index inline. 

	fromCommit, err := repo.CommitObject(plumbing.NewHash(args.from))
	if err != nil {
		return nil, nil, fmt.Errorf("accessing commit object for ref %q: %w", args.from, err)
	}
	toCommit, err := repo.CommitObject(plumbing.NewHash(args.to))
	if err != nil {
		return nil, nil, fmt.Errorf("accessing commit object for ref %q: %w", args.to, err)
	}



	// generate diff for "from"..."to"
	patch, err := fromCommit.Patch(toCommit)
	if err != nil {
		return nil, nil, fmt.Errorf("calculating diff: %w", err)
	}

	for _, fp := range patch.FilePatches() {
		oldFile, newFile := fp.Files()
		fp.Chunks()

		if oldFile != nil {
			tombstone(oldFile.Path())
		}

		if newFile != nil {
			addDocument(
				new
			)
		}

		if oldFile != nil {
			// either a deletion or a file modification
			changes = append(changes, fileChange{
				path: oldFile.Path(),
			})
		}
		

		if newFile != nil {
			// file modification or creation

			filePath := newFile.Path()

			changes = append(changes, fileChange{
				path: filePath,

				contents: func() ([]byte, error) {
					f, err := toCommit.File(filePath)
					if err != nil {
						return nil, fmt.Errorf("loading file %q from commit: %q: %w", toCommit.Hash.String(), f, err)
					}

					rc, err := f.Blob.Reader()
					if err != nil {
						return nil, fmt.Errorf("getting reader for file %q: %w", filePath, err)
					}
					defer rc.Close()

					contents, err := io.ReadAll(rc)
					if err != nil {
						return nil, fmt.Errorf("reading file contents for %q: %w", filePath, err)
					}

					// TODO: do we need the file mode here?
					return contents, nil
				},
			})
		}

	}

	return changes, cleanup, nil
}
