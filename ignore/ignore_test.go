package ignore

import (
	"bytes"
	"reflect"
	"testing"
)

func TestParseIgnoreFile(t *testing.T) {
	tests := []struct {
		ignoreFile     []byte
		wantIgnoreList []string
	}{
		{
			ignoreFile:     []byte("# ignore this \n  \n foo\n bar"),
			wantIgnoreList: []string{"foo/", "bar/"},
		},
		{
			ignoreFile:     []byte("/foo/bar \n /qux"),
			wantIgnoreList: []string{"foo/bar/", "qux/"},
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
	tests := []struct {
		path      string
		strip     int
		wantMatch bool
	}{
		{
			path:      "foo/file.go",
			wantMatch: true,
		},
		{
			path:      "foo.go",
			wantMatch: false,
		},
		{
			path:      "bar/bas.go",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		ig := Matcher{
			ignoreList: []string{"foo/", "barbas/"},
		}

		if got := ig.Match(tt.path); got != tt.wantMatch {
			t.Errorf("got %t, expected %t", got, tt.wantMatch)
		}
	}

}
