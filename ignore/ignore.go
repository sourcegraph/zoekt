// package ignore provides helpers to support ignore-files similar to .gitignore
package ignore

import (
	"bufio"
	"io"
	"strings"
)

var (
	LineComment = "#"
	IgnoreFile  = ".sourcegraph/ignore"
)

type Matcher struct {
	ignoreList []string
}

// ParseIgnoreFile parses an ignore-file according to the following rules
//
// - each line represents a path relative to the root of the repository
// - lines starting with # are ignored
// - empty lines are ignored
// - if not present, a trailing / is implicit
func ParseIgnoreFile(r io.Reader) (matcher *Matcher, error error) {
	var patterns []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// ignore empty lines
		if line == "" {
			continue
		}
		// ignore comments
		if strings.HasPrefix(line, LineComment) {
			continue
		}
		// add trailing "/" to make sure we don't match files that
		// share a prefix with a directory
		if !strings.HasSuffix(line, "/") {
			line += "/"
		}
		line = strings.TrimPrefix(line, "/")
		patterns = append(patterns, line)
	}
	return &Matcher{ignoreList: patterns}, scanner.Err()
}

// Match returns true if path has a prefix in common with any item in m.ignoreList
func (m *Matcher) Match(path string) bool {
	if len(m.ignoreList) == 0 {
		return false
	}
	for _, pattern := range m.ignoreList {
		if strings.HasPrefix(path, pattern) {
			return true
		}
	}
	return false
}
