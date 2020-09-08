package ignore

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/gobwas/glob"
)

func TestParseIgnoreFile(t *testing.T) {
	tests := []struct {
		ignoreFile     []byte
		wantIgnoreList []glob.Glob
	}{
		{
			ignoreFile: []byte("# ignore this \n  \n foo\n bar/"),
			wantIgnoreList: []glob.Glob{
				glob.MustCompile("foo**", '/'),
				glob.MustCompile("bar/**", '/')},
		},
		{
			ignoreFile: []byte("/foo/bar \n /qux \n *.go\nfoo.go"),
			wantIgnoreList: []glob.Glob{
				glob.MustCompile("foo/bar**", '/'),
				glob.MustCompile("qux**", '/'),
				glob.MustCompile("*.go", '/'),
				glob.MustCompile("foo.go", '/')},
		},
	}

	for _, tt := range tests {
		m, err := ParseIgnoreFile(bytes.NewReader(tt.ignoreFile))
		if err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(m.ignoreList, tt.wantIgnoreList) {
			t.Errorf("got %v, expected %v", m.ignoreList, tt.wantIgnoreList)
		}
	}
}

func TestIgnoreMatcher(t *testing.T) {
	ignoreFile := `
dir1/
*.go
**/data.*
`
	ig, err := ParseIgnoreFile(strings.NewReader(ignoreFile))
	if err != nil {
		t.Errorf("error in ignoreFile")
	}
	tests := []struct {
		path      string
		wantMatch bool
	}{
		{
			path:      "dir1/readme.md",
			wantMatch: true,
		},
		{
			path:      "foo.go",
			wantMatch: true,
		},
		{
			path:      "dir2/foo.go",
			wantMatch: false,
		},
		{
			path:      "dir3/data.xyz",
			wantMatch: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := ig.Match(tt.path); got != tt.wantMatch {
				t.Errorf("got %t, expected %t", got, tt.wantMatch)
			}
		})
	}
}
