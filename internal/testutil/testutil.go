// Package testutil contains helpers shared by tests across zoekt.
//
// It must only depend on the standard library so that internal tests of any
// zoekt package, including index, can import it without an import cycle.
package testutil

import (
	"bytes"
	"flag"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Main is a TestMain that discards log output unless tests run with -v.
func Main(m *testing.M) {
	flag.Parse()
	if !testing.Verbose() {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

// GitEnv returns the environment to run git with in tests. It ignores the
// user's global and system git config and sets a fixed author and committer,
// so git behaves the same on every machine.
func GitEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=",
		"GIT_CONFIG_SYSTEM=",
		"GIT_COMMITTER_NAME=Kierkegaard",
		"GIT_COMMITTER_EMAIL=soren@apache.com",
		"GIT_AUTHOR_NAME=Kierkegaard",
		"GIT_AUTHOR_EMAIL=soren@apache.com",
	)
}

// RunGit runs git with args in dir using GitEnv and returns its stdout. It
// fails tb if git fails.
func RunGit(tb testing.TB, dir string, args ...string) string {
	tb.Helper()

	var stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = GitEnv()
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		tb.Fatalf("git %s: %v\n%s%s", strings.Join(args, " "), err, out, stderr.Bytes())
	}
	return string(out)
}

// RequireCTags returns the universal-ctags binary from $CTAGS_COMMAND or
// $PATH. If it is missing, RequireCTags fails tb on CI, where ctags is
// installed, and skips tb elsewhere.
func RequireCTags(tb testing.TB) string {
	tb.Helper()

	if bin := os.Getenv("CTAGS_COMMAND"); bin != "" {
		return bin
	}
	if bin, err := exec.LookPath("universal-ctags"); err == nil {
		return bin
	}
	if os.Getenv("CI") != "" {
		tb.Fatal("universal-ctags is missing")
	}
	tb.Skip("universal-ctags is missing")
	return ""
}

// RequireScipCTags returns the scip-ctags binary from $SCIP_CTAGS_COMMAND or
// $PATH, and skips tb if it is missing. CI does not install scip-ctags.
func RequireScipCTags(tb testing.TB) string {
	tb.Helper()

	if bin := os.Getenv("SCIP_CTAGS_COMMAND"); bin != "" {
		return bin
	}
	if bin, err := exec.LookPath("scip-ctags"); err == nil {
		return bin
	}
	tb.Skip("scip-ctags is missing")
	return ""
}

// CopyFiles copies each file in srcs into dstDir and returns the paths of the
// copies.
func CopyFiles(tb testing.TB, dstDir string, srcs ...string) []string {
	tb.Helper()

	var dsts []string
	for _, src := range srcs {
		data, err := os.ReadFile(src)
		if err != nil {
			tb.Fatal(err)
		}
		dst := filepath.Join(dstDir, filepath.Base(src))
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			tb.Fatal(err)
		}
		dsts = append(dsts, dst)
	}
	return dsts
}
