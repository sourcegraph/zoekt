// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gitindex

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strconv"

	"github.com/go-git/go-git/v5/plumbing"
)

// catfileReader provides streaming access to git blob objects via a pipelined
// "git cat-file --batch --buffer" process. A writer goroutine feeds all blob
// SHAs to stdin while the caller reads responses one at a time, similar to
// archive/tar.Reader.
//
// The --buffer flag switches git's output from per-object flush (write_or_die)
// to libc stdio buffering (fwrite), reducing syscalls. After stdin EOF, git
// calls fflush(stdout) to deliver any remaining output.
//
// Usage:
//
//	cr, err := newCatfileReader(repoDir, ids)
//	if err != nil { ... }
//	defer cr.Close()
//
//	for {
//	    size, missing, err := cr.Next()
//	    if err == io.EOF { break }
//	    if missing { continue }
//	    if size > maxSize { continue } // unread bytes auto-skipped
//	    content := make([]byte, size)
//	    io.ReadFull(cr, content)
//	}
type catfileReader struct {
	cmd      *exec.Cmd
	reader   *bufio.Reader
	writeErr <-chan error

	// pending tracks unread content bytes + trailing LF for the current
	// entry. Next() discards any pending bytes before reading the next header.
	pending int64
}

// newCatfileReader starts a "git cat-file --batch --buffer" process and feeds
// all ids to its stdin via a background goroutine. The caller must call Close
// when done.
func newCatfileReader(repoDir string, ids []plumbing.Hash) (*catfileReader, error) {
	cmd := exec.Command("git", "cat-file", "--batch", "--buffer")
	cmd.Dir = repoDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start git cat-file: %w", err)
	}

	// Writer goroutine: feed all SHAs then close stdin to trigger flush.
	writeErr := make(chan error, 1)
	go func() {
		defer stdin.Close()
		bw := bufio.NewWriterSize(stdin, 64*1024)
		var hexBuf [41]byte
		hexBuf[40] = '\n'
		for _, id := range ids {
			hex.Encode(hexBuf[:40], id[:])
			if _, err := bw.Write(hexBuf[:]); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- bw.Flush()
	}()

	return &catfileReader{
		cmd:      cmd,
		reader:   bufio.NewReaderSize(stdout, 512*1024),
		writeErr: writeErr,
	}, nil
}

// Next advances to the next blob entry. It returns the blob's size and whether
// it is missing. Any unread content from the previous entry is automatically
// discarded. Returns io.EOF when all entries have been consumed.
//
// After Next returns successfully with missing=false, call Read to consume the
// blob content, or call Next again to skip it.
func (cr *catfileReader) Next() (size int64, missing bool, err error) {
	// Discard unread content from the previous entry.
	if cr.pending > 0 {
		if _, err := cr.reader.Discard(int(cr.pending)); err != nil {
			return 0, false, fmt.Errorf("discard pending bytes: %w", err)
		}
		cr.pending = 0
	}

	headerBytes, err := cr.reader.ReadBytes('\n')
	if err != nil {
		if err == io.EOF {
			return 0, false, io.EOF
		}
		return 0, false, fmt.Errorf("read header: %w", err)
	}
	header := headerBytes[:len(headerBytes)-1] // trim \n

	if bytes.HasSuffix(header, []byte(" missing")) {
		return 0, true, nil
	}

	// Parse size from "<oid> <type> <size>".
	lastSpace := bytes.LastIndexByte(header, ' ')
	if lastSpace == -1 {
		return 0, false, fmt.Errorf("unexpected header: %q", header)
	}
	size, err = strconv.ParseInt(string(header[lastSpace+1:]), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse size from %q: %w", header, err)
	}

	// Track pending bytes: content + trailing LF.
	cr.pending = size + 1
	return size, false, nil
}

// Read reads from the current blob's content. Implements io.Reader. Returns
// io.EOF when the blob's content has been fully read (the trailing LF
// delimiter is consumed automatically).
func (cr *catfileReader) Read(p []byte) (int, error) {
	if cr.pending <= 0 {
		return 0, io.EOF
	}

	// Don't read into the trailing LF byte — reserve it.
	contentRemaining := cr.pending - 1
	if contentRemaining <= 0 {
		// Only the trailing LF remains; consume it and signal EOF.
		if _, err := cr.reader.ReadByte(); err != nil {
			return 0, fmt.Errorf("read trailing LF: %w", err)
		}
		cr.pending = 0
		return 0, io.EOF
	}

	// Limit the read to the remaining content bytes.
	if int64(len(p)) > contentRemaining {
		p = p[:contentRemaining]
	}
	n, err := cr.reader.Read(p)
	cr.pending -= int64(n)
	if err != nil {
		return n, err
	}

	// If we've consumed all content bytes, also consume the trailing LF.
	if cr.pending == 1 {
		if _, err := cr.reader.ReadByte(); err != nil {
			return n, fmt.Errorf("read trailing LF: %w", err)
		}
		cr.pending = 0
	}

	return n, nil
}

// Close shuts down the cat-file process and waits for it to exit.
func (cr *catfileReader) Close() error {
	// Drain stdout so git can flush and exit without blocking.
	_, _ = io.Copy(io.Discard, cr.reader)
	// Wait for writer goroutine.
	<-cr.writeErr
	return cr.cmd.Wait()
}
