package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func archiveLsTree(w io.Writer, repo *git.Repository, tree *object.Tree, opts *archiveOpts) (err error) {
	cmd := exec.Command("git", "ls-tree", "-r", "-l", "-t", "-z", tree.Hash.String())
	r, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer r.Close()

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

	entries := bufio.NewScanner(r)
	entries.Split(scanNull)

	for entries.Scan() {
		line := entries.Bytes()
		// PERF this allocates much less than bytes.Split
		next := func() []byte {
			i := bytes.IndexByte(line, ' ')
			if i < 0 {
				pre := line
				line = nil
				return pre
			}
			pre := line[:i]
			line = bytes.TrimLeft(line[i+1:], " ")
			return pre
		}

		// %(objectmode) %(objecttype) %(objectname) %(objectsize:padded)%x09%(path)
		modeRaw := next()
		typ := next()
		hash := next()

		_ = hash

		// remaining: %(objectsize:padded)\t%(path)
		//
		// size is left padded with space
		line = bytes.TrimLeft(line, " ")
		i := bytes.IndexByte(line, '\t')
		if i < 0 {
			return fmt.Errorf("malformed ls-tree entry: %q", entries.Text())
		}
		sizeRaw := line[:i]
		path := string(line[i+1:])

		if opts.Ignore(path) {
			continue
		}

		if bytes.Equal(typ, []byte("blob")) {
			mode, _ := strconv.ParseInt(string(modeRaw), 8, 64)
			size, _ := strconv.ParseInt(string(sizeRaw), 10, 64)

			hdr := tar.Header{
				Typeflag: tar.TypeReg,
				Name:     path,
				Mode:     mode & 0777,
				Size:     size,
				Format:   tar.FormatPAX, // TODO ?
			}

			if reason := opts.SkipContent(&hdr); reason != "" {
				hdr.PAXRecords = map[string]string{"SG.skip": reason}
			}

			hdr.Size = 0
			if err := tw.WriteHeader(&hdr); err != nil {
				return err
			}

		} else if bytes.Equal(typ, []byte("tree")) {
			hdr := tar.Header{
				Typeflag: tar.TypeDir,
				Name:     path,
				Mode:     0777,
				Format:   tar.FormatPAX, // TODO ?
			}
			if err := tw.WriteHeader(&hdr); err != nil {
				return err
			}
		} else {
			log.Printf("unexpected type on line: %q", entries.Text())
			continue
		}
	}

	if err := entries.Err(); err != nil {
		return err
	}

	if err := tw.Close(); err != nil {
		return err
	}

	done = true
	return cmd.Wait()
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
