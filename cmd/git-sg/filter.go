package main

import (
	"archive/tar"
	"io"
	"os/exec"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func archiveFilter(w io.Writer, repo *git.Repository, tree *object.Tree, opts *archiveOpts) (err error) {
	// 32*1024 is the same size used by io.Copy
	buf := make([]byte, 32*1024)

	cmd := exec.Command("git", "archive", "--worktree-attributes", "--format=tar", tree.Hash.String(), "--")
	r, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer r.Close()

	tr := tar.NewReader(r)
	tw := tar.NewWriter(w)

	err = cmd.Start()
	if err != nil {
		return err
	}

	done := false
	defer func() {
		if done {
			return
		}
		err2 := cmd.Process.Kill()
		if err == nil {
			err = err2
		}
	}()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if opts.Ignore(hdr.Name) {
			continue
		} else if reason := opts.SkipContent(hdr); reason != "" {
			hdr.Size = 0
			hdr.PAXRecords = map[string]string{"SG.skip": reason}
			hdr.Format = tar.FormatPAX
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			continue
		}

		tw.WriteHeader(hdr)
		if _, err := io.CopyBuffer(tw, tr, buf); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return err
	}

	done = true
	return cmd.Wait()
}
