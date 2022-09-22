package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"

	"github.com/go-git/go-git/v5/plumbing"
)

// gitCatFileBatch is a wrapper around a git-cat-file --batch-command process.
// This provides an efficient means to interact with the git object store of a
// repository.
type gitCatFileBatch struct {
	cmd      *exec.Cmd
	in       *bufio.Writer
	inCloser io.Closer
	out      *gitCatFileBatchReader

	// hashBuf is encoded to for plumbing.Hash
	hashBuf [20 * 2]byte
}

type missingError struct {
	ref string
}

func (e *missingError) Error() string {
	return e.ref + " missing"
}

func isMissingError(err error) bool {
	var e *missingError
	return errors.As(err, &e)
}

// startGitCatFileBatch returns a gitCatFileBatch for the repository at dir.
//
// Callers must ensure to call gitCatFileBatch.Close() to ensure the
// associated subprocess and file descriptors are cleaned up.
func startGitCatFileBatch(dir string) (_ *gitCatFileBatch, err error) {
	cmd := exec.Command("git", "cat-file", "--batch-command")
	cmd.Dir = dir

	closeIfErr := func(closer io.Closer) {
		if err != nil {
			closer.Close()
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer closeIfErr(stdin)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer closeIfErr(stdin)

	// TODO should capture somehow and put into error
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &gitCatFileBatch{
		cmd:      cmd,
		in:       bufio.NewWriter(stdin),
		inCloser: stdin,
		out:      newGitCatFileBatchReader(stdout),
	}, nil
}

type gitCatFileBatchInfo struct {
	Hash plumbing.Hash
	Type plumbing.ObjectType
	Size int64
}

func (g *gitCatFileBatch) InfoString(ref string) (gitCatFileBatchInfo, error) {
	g.in.WriteString("info ")
	g.in.WriteString(ref)
	g.in.WriteByte('\n')
	if err := g.in.Flush(); err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	info, err := g.out.Info()
	if err != nil && !isMissingError(err) { // missingError is recoverable
		g.kill()
	}
	return info, err
}

func (g *gitCatFileBatch) Info(hash plumbing.Hash) (gitCatFileBatchInfo, error) {
	g.in.WriteString("info ")
	g.writeHash(hash)
	g.in.WriteByte('\n')
	if err := g.in.Flush(); err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	info, err := g.out.Info()
	if err != nil && !isMissingError(err) { // missingError is recoverable
		g.kill()
	}
	return info, err
}

func (g *gitCatFileBatch) ContentsString(ref string) (gitCatFileBatchInfo, error) {
	g.in.WriteString("contents ")
	g.in.WriteString(ref)
	g.in.WriteByte('\n')
	if err := g.in.Flush(); err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	info, err := g.out.Contents()
	if err != nil && !isMissingError(err) { // missingError is recoverable
		g.kill()
	}
	return info, err
}

func (g *gitCatFileBatch) Contents(hash plumbing.Hash) (gitCatFileBatchInfo, error) {
	g.in.WriteString("contents ")
	g.writeHash(hash)
	g.in.WriteByte('\n')
	if err := g.in.Flush(); err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	info, err := g.out.Contents()
	if err != nil && !isMissingError(err) { // missingError is recoverable
		g.kill()
	}
	return info, err
}

func (g *gitCatFileBatch) Read(b []byte) (int, error) {
	return g.out.Read(b)
}

func (g *gitCatFileBatch) writeHash(hash plumbing.Hash) {
	hex.Encode(g.hashBuf[:], hash[:])
	g.in.Write(g.hashBuf[:])
}

type gitCatFileBatchReader struct {
	out       *bufio.Reader
	outCloser io.Closer

	// readerN is the amount left to read for Read. Note: git-cat-file always
	// has a trailing new line, so this will always be the size of an object +
	// 1.
	readerN int64
}

func newGitCatFileBatchReader(r io.ReadCloser) *gitCatFileBatchReader {
	return &gitCatFileBatchReader{
		out:       bufio.NewReader(r),
		outCloser: r,
	}
}

func (g *gitCatFileBatchReader) Info() (gitCatFileBatchInfo, error) {
	if err := g.Discard(); err != nil {
		g.Close()
		return gitCatFileBatchInfo{}, err
	}

	line, err := g.out.ReadSlice('\n')
	if err != nil {
		g.Close()
		return gitCatFileBatchInfo{}, err
	}

	info, err := parseGitCatFileBatchInfoLine(line)
	if err != nil {
		if !isMissingError(err) { // missingError is recoverable
			g.Close()
		}
		return gitCatFileBatchInfo{}, err
	}

	// Info has nothing following to read
	g.readerN = 0

	return info, nil
}

func (g *gitCatFileBatchReader) Contents() (gitCatFileBatchInfo, error) {
	info, err := g.Info()
	if err != nil {
		return info, err
	}

	// Still have the contents to read and an extra newline
	g.readerN = info.Size + 1

	return info, nil
}

func (g *gitCatFileBatchReader) Read(p []byte) (n int, err error) {
	// We avoid reading the final byte (a newline). That will be handled by
	// discard.
	if g.readerN <= 1 {
		return 0, io.EOF
	}
	if max := g.readerN - 1; int64(len(p)) > max {
		p = p[0:max]
	}
	n, err = g.out.Read(p)
	g.readerN -= int64(n)
	return
}

// Discard should be called before parsing a response to flush out any unread
// data since the last command.
func (g *gitCatFileBatchReader) Discard() error {
	if g.readerN > 0 {
		n, err := g.out.Discard(int(g.readerN))
		g.readerN -= int64(n)
		return err
	}
	return nil
}

func (g *gitCatFileBatchReader) Close() error {
	return g.outCloser.Close()
}

// parseGitCatFileBatchInfoLine parses the info line from git-cat-file. It
// expects the default format of:
//
//	<oid> SP <type> SP <size> LF
func parseGitCatFileBatchInfoLine(line []byte) (gitCatFileBatchInfo, error) {
	line = bytes.TrimRight(line, "\n")
	origLine := line

	if bytes.HasSuffix(line, []byte(" missing")) {
		ref := bytes.TrimSuffix(line, []byte(" missing"))
		return gitCatFileBatchInfo{}, &missingError{ref: string(ref)}
	}

	// PERF this allocates much less than bytes.Split
	next := func() []byte {
		i := bytes.IndexByte(line, ' ')
		if i < 0 {
			pre := line
			line = nil
			return pre
		}
		pre := line[:i]
		line = line[i+1:]
		return pre
	}

	info := gitCatFileBatchInfo{}

	var err error
	_, err = hex.Decode(info.Hash[:], next())
	if err != nil {
		return info, fmt.Errorf("unexpected git-cat-file --batch info line %q: %w", string(origLine), err)
	}

	info.Type, err = plumbing.ParseObjectType(string(next()))
	if err != nil {
		return info, fmt.Errorf("unexpected git-cat-file --batch info line %q: %w", string(origLine), err)
	}

	info.Size, err = strconv.ParseInt(string(next()), 10, 64)
	if err != nil {
		return info, fmt.Errorf("unexpected git-cat-file --batch info line %q: %w", string(origLine), err)
	}

	return info, nil
}

func (g *gitCatFileBatch) Close() (err error) {
	defer func() {
		if err != nil {
			g.kill()
		}
	}()

	if err := g.out.Discard(); err != nil {
		return err
	}

	// This Close will tell git to shutdown
	if err := g.inCloser.Close(); err != nil {
		return err
	}

	// Drain and check we have no output left (to detect mistakes)
	if n, err := io.Copy(io.Discard, g.out); err != nil {
		return err
	} else if n > 0 {
		log.Printf("unexpected %d bytes of remaining output when calling close", n)
	}

	if err := g.out.Close(); err != nil {
		return err
	}

	return g.cmd.Wait()
}

func (g *gitCatFileBatch) kill() {
	_ = g.cmd.Process.Kill()
	_ = g.inCloser.Close()
	_ = g.out.Close()
}
