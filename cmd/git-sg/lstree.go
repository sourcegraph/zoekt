package main

import (
	"bytes"
	"fmt"
	"io"
	"syscall"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func archiveLsTree(w io.Writer, repo *git.Repository, tree *object.Tree, opts *archiveOpts) (err error) {
	fmt.Println(syscall.PathMax)
	return nil
}

// scanNull is a split function for bufio.Scanner that returns each item of
// text as split by the null character. It will not include the null.
func scanNull(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[0:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	// Request more data.
	return 0, nil, nil
}
