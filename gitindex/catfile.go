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

// blobResult holds the result of reading a single blob from a pipelined
// cat-file --batch --buffer process.
type blobResult struct {
	ID      plumbing.Hash
	Content []byte
	Size    int64
	Missing bool
	Err     error
}

// readBlobsPipelined reads all blobs for the given IDs using a single
// "git cat-file --batch --buffer" process. A writer goroutine feeds SHAs
// to stdin while the main goroutine reads responses from stdout, forming a
// concurrent pipeline. The --buffer flag switches git's output from per-object
// flush (write_or_die) to libc stdio buffering (fwrite), reducing syscalls.
// After stdin EOF, git calls fflush(stdout) to deliver any remaining output.
// Results are returned in the same order as ids.
func readBlobsPipelined(repoDir string, ids []plumbing.Hash) ([]blobResult, error) {
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
	// Uses bufio.Writer to coalesce small writes into fewer syscalls.
	// Stack-allocated hex buffer avoids per-SHA heap allocation.
	writeErr := make(chan error, 1)
	go func() {
		defer stdin.Close()
		bw := bufio.NewWriterSize(stdin, 64*1024) // 64KB write buffer
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

	// Reader: consume all responses in order.
	// Manual header parsing avoids SplitN allocation.
	reader := bufio.NewReaderSize(stdout, 512*1024)
	results := make([]blobResult, len(ids))
	var readErr error

	for i, id := range ids {
		results[i].ID = id

		headerBytes, err := reader.ReadBytes('\n')
		if err != nil {
			readErr = fmt.Errorf("read header for %s: %w", id, err)
			results[i].Err = readErr
			break
		}
		header := headerBytes[:len(headerBytes)-1] // trim \n

		if bytes.HasSuffix(header, []byte(" missing")) {
			results[i].Missing = true
			continue
		}

		// Parse size from "<oid> <type> <size>".
		lastSpace := bytes.LastIndexByte(header, ' ')
		if lastSpace == -1 {
			readErr = fmt.Errorf("unexpected header: %q", header)
			results[i].Err = readErr
			break
		}
		size, err := strconv.ParseInt(string(header[lastSpace+1:]), 10, 64)
		if err != nil {
			readErr = fmt.Errorf("parse size from %q: %w", header, err)
			results[i].Err = readErr
			break
		}
		results[i].Size = size

		// Read exactly size bytes into a dedicated slice (must survive
		// until consumed by builder.Add). Exact-size avoids allocator
		// rounding waste (e.g. make(4097) → 8192 bytes).
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			readErr = fmt.Errorf("read content (%d bytes): %w", size, err)
			results[i].Err = readErr
			break
		}
		results[i].Content = content

		// Consume trailing LF delimiter.
		if _, err := reader.ReadByte(); err != nil {
			readErr = fmt.Errorf("read trailing LF: %w", err)
			results[i].Err = readErr
			break
		}
	}

	// Mark all unprocessed results as failed if we broke out early.
	if readErr != nil {
		for j := range results {
			if results[j].Err == nil && results[j].Content == nil && !results[j].Missing {
				results[j].Err = readErr
			}
		}
	}

	// Drain stdout so git can exit without blocking on a full pipe buffer.
	_, _ = io.Copy(io.Discard, reader)

	// Wait for writer goroutine to finish.
	wErr := <-writeErr

	// Wait for the git process to exit.
	waitErr := cmd.Wait()

	// Return the first meaningful error.
	if readErr != nil {
		return results, readErr
	}
	if wErr != nil {
		return results, fmt.Errorf("write to cat-file: %w", wErr)
	}
	if waitErr != nil {
		return results, waitErr
	}

	return results, nil
}
