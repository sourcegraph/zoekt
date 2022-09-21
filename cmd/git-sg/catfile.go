package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"

	"github.com/go-git/go-git/v5/plumbing"
)

type gitCatFileBatch struct {
	cmd       *exec.Cmd
	in        *bufio.Writer
	inCloser  io.Closer
	out       *bufio.Reader
	outCloser io.Closer

	// readerN is the amount left to read for Read. Note: git-cat-file always
	// has a trailing new line, so this will always be the size of an object +
	// 1.
	readerN int64
}

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
		cmd:       cmd,
		in:        bufio.NewWriter(stdin),
		inCloser:  stdin,
		out:       bufio.NewReader(stdout),
		outCloser: stdout,
	}, nil
}

type gitCatFileBatchInfo struct {
	Hash plumbing.Hash
	Type plumbing.ObjectType
	Size int64
}

func (g *gitCatFileBatch) Info(ref string) (gitCatFileBatchInfo, error) {
	g.in.WriteString("info ")
	g.in.WriteString(ref)
	g.in.WriteByte('\n')
	if err := g.in.Flush(); err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	if err := g.discard(); err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	line, err := g.out.ReadSlice('\n')
	if err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	info, err := parseGitCatFileBatchInfoLine(line)
	if err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	g.readerN = 0

	return info, nil
}

func (g *gitCatFileBatch) Contents(ref string) (gitCatFileBatchInfo, error) {
	g.in.WriteString("contents ")
	g.in.WriteString(ref)
	g.in.WriteByte('\n')
	if err := g.in.Flush(); err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	if err := g.discard(); err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	line, err := g.out.ReadSlice('\n')
	if err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	info, err := parseGitCatFileBatchInfoLine(line)
	if err != nil {
		g.kill()
		return gitCatFileBatchInfo{}, err
	}

	g.readerN = info.Size + 1

	return info, nil
}

func (g *gitCatFileBatch) Read(p []byte) (n int, err error) {
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

// discard should be called before parsing a response to flush out any unread
// data since the last command.
func (g *gitCatFileBatch) discard() error {
	if g.readerN > 0 {
		n, err := g.out.Discard(int(g.readerN))
		g.readerN -= int64(n)
		return err
	}
	return nil
}

// parseGitCatFileBatchInfoLine parses the info line from git-cat-file. It
// expects the default format of:
//
//  <oid> SP <type> SP <size> LF
func parseGitCatFileBatchInfoLine(line []byte) (gitCatFileBatchInfo, error) {
	line = bytes.TrimRight(line, "\n")
	origLine := line

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

	if err := g.discard(); err != nil {
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

	if err := g.outCloser.Close(); err != nil {
		return err
	}

	return g.cmd.Wait()
}

func (g *gitCatFileBatch) kill() {
	_ = g.cmd.Process.Kill()
	_ = g.inCloser.Close()
	_ = g.outCloser.Close()
}
