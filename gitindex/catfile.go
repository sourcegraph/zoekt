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
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/go-git/go-git/v5/plumbing"
)

type catfileReaderOptions struct {
	filterSpec string
}

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
//	cr, err := newCatfileReader(repoDir, ids, catfileReaderOptions{})
//	if err != nil { ... }
//	defer cr.Close()
//
//	for {
//	    size, missing, excluded, err := cr.Next()
//	    if err == io.EOF { break }
//	    if missing { continue }
//	    if excluded { continue }
//	    if size > maxSize { continue } // unread bytes auto-skipped
//	    content := make([]byte, size)
//	    io.ReadFull(cr, content)
//	}
type catfileReader struct {
	cmd      *exec.Cmd
	reader   *bufio.Reader
	writeErr <-chan error
	stderr   <-chan catfileStderrResult

	procOnce sync.Once
	procMu   sync.Mutex
	procErr  error

	// pending tracks unread content bytes + trailing LF for the current
	// entry. Next() discards any pending bytes before reading the next header.
	pending int
	eof     bool

	closeOnce sync.Once
	closeErr  error
}

// newCatfileReader starts a "git cat-file --batch --buffer" process and feeds
// all ids to its stdin via a background goroutine. The caller must call Close
// when done. Pass a zero-value catfileReaderOptions when no options are needed.
func newCatfileReader(repoDir string, ids []plumbing.Hash, opts catfileReaderOptions) (*catfileReader, error) {
	args := []string{"cat-file", "--batch", "--buffer"}
	if opts.filterSpec != "" {
		args = append(args, "--filter="+opts.filterSpec)
	}

	cmd := exec.Command("git", args...)
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

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("start git cat-file: %w", err)
	}

	writeErr := make(chan error, 1)
	stderrDone := make(chan catfileStderrResult, 1)

	cr := &catfileReader{
		cmd:      cmd,
		reader:   bufio.NewReaderSize(stdout, 512*1024),
		writeErr: writeErr,
		stderr:   stderrDone,
	}
	go func() {
		stderrBytes, stderrReadErr := io.ReadAll(stderr)
		stderrDone <- catfileStderrResult{data: stderrBytes, err: stderrReadErr}
		close(stderrDone)
	}()

	// Writer goroutine: feed all SHAs then close stdin to trigger flush.
	go func() {
		defer close(writeErr)
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

	return cr, nil
}

// Next advances to the next blob entry. It returns the blob's size and whether
// it is missing or excluded by the configured filter. Any unread content from
// the previous entry is automatically discarded. Returns io.EOF when all
// entries have been consumed.
//
// After Next returns successfully with missing=false and excluded=false, call
// Read to consume the blob content, or call Next again to skip it.
func (cr *catfileReader) Next() (size int, missing bool, excluded bool, err error) {
	if cr.eof {
		return 0, false, false, io.EOF
	}

	// Discard unread content from the previous entry.
	if cr.pending > 0 {
		if _, err := cr.reader.Discard(cr.pending); err != nil {
			return 0, false, false, cr.wrapReadError("discard pending bytes", err)
		}
		cr.pending = 0
	}

	headerBytes, err := cr.reader.ReadBytes('\n')
	if err != nil {
		if err == io.EOF {
			if procErr := cr.waitProcess(); procErr != nil {
				return 0, false, false, procErr
			}
			cr.eof = true
			return 0, false, false, io.EOF
		}
		return 0, false, false, fmt.Errorf("read header: %w", err)
	}
	header := headerBytes[:len(headerBytes)-1] // trim \n
	if bytes.HasSuffix(header, []byte(" missing")) {
		return 0, true, false, nil
	}

	if bytes.HasSuffix(header, []byte(" excluded")) {
		return 0, false, true, nil
	}

	// Parse size from "<oid> <type> <size>".
	lastSpace := bytes.LastIndexByte(header, ' ')
	if lastSpace == -1 {
		return 0, false, false, fmt.Errorf("unexpected header: %q", header)
	}
	size, err = strconv.Atoi(string(header[lastSpace+1:]))
	if err != nil {
		return 0, false, false, fmt.Errorf("parse size from %q: %w", header, err)
	}

	// Track pending bytes: content + trailing LF.
	cr.pending = size + 1
	return size, false, false, nil
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
			return 0, cr.wrapReadError("read trailing LF", err)
		}
		cr.pending = 0
		return 0, io.EOF
	}

	// Limit the read to the remaining content bytes.
	if len(p) > contentRemaining {
		p = p[:contentRemaining]
	}
	n, err := cr.reader.Read(p)
	cr.pending -= n
	if err != nil {
		return n, cr.wrapReadError("read blob content", err)
	}

	// If we've consumed all content bytes, also consume the trailing LF.
	if cr.pending == 1 {
		if _, err := cr.reader.ReadByte(); err != nil {
			return n, cr.wrapReadError("read trailing LF", err)
		}
		cr.pending = 0
	}

	return n, nil
}

// Close shuts down the cat-file process and waits for it to exit.
// It is safe to call Close multiple times or concurrently.
func (cr *catfileReader) Close() error {
	cr.closeOnce.Do(func() {
		// Kill first to avoid blocking on drain when there are many
		// unconsumed entries. Gitaly uses the same kill-first pattern.
		_ = cr.cmd.Process.Kill()
		// Drain any buffered stdout so the pipe closes cleanly.
		// Must complete before cmd.Wait(), which closes the pipe.
		_, _ = io.Copy(io.Discard, cr.reader)
		// Wait for writer goroutine (unblocks via broken pipe from Kill).
		<-cr.writeErr
		err := cr.waitProcess()
		// Suppress the expected "signal: killed" error from our own Kill().
		if isKilledErr(err) {
			err = nil
		}
		cr.closeErr = err
	})
	return cr.closeErr
}

func (cr *catfileReader) waitProcess() error {
	cr.procOnce.Do(func() {
		stderr := <-cr.stderr
		waitErr := cr.cmd.Wait()

		cr.procMu.Lock()
		cr.procErr = formatCatfileProcessErr(waitErr, stderr.data, stderr.err)
		cr.procMu.Unlock()
	})

	cr.procMu.Lock()
	defer cr.procMu.Unlock()

	return cr.procErr
}

func (cr *catfileReader) wrapReadError(context string, err error) error {
	if err == io.EOF {
		if procErr := cr.waitProcess(); procErr != nil {
			return procErr
		}
	}

	return fmt.Errorf("%s: %w", context, err)
}

func formatCatfileProcessErr(waitErr error, stderr []byte, stderrReadErr error) error {
	if waitErr == nil {
		if stderrReadErr != nil {
			return fmt.Errorf("read git cat-file stderr: %w", stderrReadErr)
		}
		return nil
	}

	stderrText := strings.TrimSpace(string(stderr))
	if stderrText == "" {
		return fmt.Errorf("git cat-file exited unsuccessfully: %w", waitErr)
	}

	return fmt.Errorf("git cat-file exited unsuccessfully: %w: %s", waitErr, stderrText)
}

type catfileStderrResult struct {
	data []byte
	err  error
}

// isKilledErr reports whether err is an exec.ExitError caused by SIGKILL.
func isKilledErr(err error) bool {
	var exitErr *exec.ExitError
	ok := errors.As(err, &exitErr)
	if !ok {
		return false
	}
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && ws.Signal() == syscall.SIGKILL
}
