// package ignore provides helpers to support ignore-files similar to .gitignore
package ignore

import (
	"bufio"
	"io"
	"strings"
)

var (
	LineComment = "#"
	IgnoreFile  = ".sourcegraph/sourcegraphignore"
)

type IgnoreMatcher struct {
	IgnoreList []string
	Strip      int
}

// StripComponents removes the specified number of leading path
// elements. Pathnames with fewer elements will return the empty string.
func StripComponents(path string, count int) string {
	for i := 0; path != "" && i < count; i++ {
		i := strings.Index(path, "/")
		if i < 0 {
			return ""
		}
		path = path[i+1:]
	}
	return path
}

// ParseIgnoreFile parses an ignore-file according to the following rules
//
// - each line represents a path relative to the root of the repository
// - lines starting with # are ignored
// - empty lines are ignored
// - if not present, a trailing / is implicit
func ParseIgnoreFile(r io.Reader) (patterns []string, error error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// ignore empty lines
		if len(line) == 0 {
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
	return patterns, scanner.Err()
}

// Match returns true if path has a prefix in common with any item in m.IgnoreList
func (m *IgnoreMatcher) Match(path string) bool {
	if len(m.IgnoreList) == 0 {
		return false
	}
	for _, pattern := range m.IgnoreList {
		if strings.HasPrefix(StripComponents(path, m.Strip), pattern) {
			return true
		}
	}
	return false
}
