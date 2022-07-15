package main

import (
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/zoekt"
	"github.com/sourcegraph/go-diff/diff"
)

func Test_ParseDiffHunkNew(t *testing.T) {
	var tests = []struct {
		name string
		in   diff.Hunk
		want string
	}{
		{
			name: "no changes - no newline at end", // shouldn't happen in reality
			in: diff.Hunk{
				Body: []byte(" Hello\n World"),
			},
			want: "Hello\nWorld",
		},
		{
			name: "no changes - newline at end", // shouldn't happen in reality
			in: diff.Hunk{
				Body: []byte(" Hello\n World\n"),
			},
			want: "Hello\nWorld\n",
		},
		{
			name: "only additions",
			in: diff.Hunk{
				Body: []byte(` Hello
+Wonderful
 World
+!`),
			},
			want: `Hello
Wonderful
World
!`,
		},
		{
			name: "additions and removals",
			in: diff.Hunk{
				Body: []byte(`-Hello
+Goodbye
 World`),
			},
			want: `Goodbye
World`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDiffHuskNew(&tt.in)
			if string(got) != tt.want {
				t.Errorf("Incorrect diff parse want:\n%s\ngot:\n%s", string(got), tt.want)
			}
		})
	}
}

func Test_ParseDiffHunkOrig(t *testing.T) {
	var tests = []struct {
		name string
		in   diff.Hunk
		want string
	}{
		{
			name: "no changes - no newline at end", // shouldn't happen in reality
			in: diff.Hunk{
				Body: []byte(" Hello\n World"),
			},
			want: "Hello\nWorld",
		},
		{
			name: "no changes - newline at end", // shouldn't happen in reality
			in: diff.Hunk{
				Body: []byte(" Hello\n World\n"),
			},
			want: "Hello\nWorld\n",
		},
		{
			name: "only additions",
			in: diff.Hunk{
				Body: []byte(` Hello
+Wonderful
 World
+!`),
			},
			want: `Hello
World
`,
		},
		{
			name: "additions and removals",
			in: diff.Hunk{
				Body: []byte(`-Hello
+Goodbye
 World`),
			},
			want: `Hello
World`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDiffHuskOrig(&tt.in)
			if string(got) != tt.want {
				t.Errorf("Incorrect diff parse want:\n%s\ngot:\n%s", string(got), tt.want)
			}
		})
	}
}

func Test_ComputeGitHash(t *testing.T) {
	var tests = []struct {
		name string
		in   zoekt.Document
		want plumbing.Hash
	}{
		{
			name: "file without newline at end",
			in: zoekt.Document{
				Name:    "a.txt",
				Content: []byte("Hello, World!"),
			},
			want: plumbing.NewHash("b45ef6fec89518d314f546fd6c3025367b721684"),
		},
		{
			name: "file with newline at end",
			in: zoekt.Document{
				Name:    "b.txt",
				Content: []byte("Hello, World!\n"),
			},
			want: plumbing.NewHash("8ab686eafeb1f44702738c8b0f24f2567c36da6d"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := plumbing.ComputeHash(plumbing.BlobObject, tt.in.Content)
			if got != tt.want {
				t.Errorf("Hash mismatch got %s, wanted %s", got, tt.want)
			}
		})
	}
}

func Test_ParseGitHashFromDiff(t *testing.T) {
	var tests = []struct {
		name string
		in   diff.FileDiff
		want [2]string
	}{
		{
			name: "",
			in:   diff.FileDiff{Extended: []string{"diff --git go.mod go.mod", "index 0862d4714f9faf7b1b2c2ff3645c18d3925a2c3a..d479a7d884e72e4bfe326e054474b7720bec7cae 100644"}},
			want: [2]string{"0862d4714f9faf7b1b2c2ff3645c18d3925a2c3a", "d479a7d884e72e4bfe326e054474b7720bec7cae"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOld, gotNew := parseGitHashFromDiff(&tt.in)
			if gotOld.String() != tt.want[0] {
				t.Errorf("Hash mismatch got %s, wanted %s", gotOld, tt.want)
			}
			if gotNew.String() != tt.want[1] {
				t.Errorf("Hash mismatch got %s, wanted %s", gotNew, tt.want)
			}
		})
	}
}
