package main

import (
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/zoekt/gitindex"
)

func TestParseBranches(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []gitindex.Branch
		wantErr  bool
	}{
		{
			name:     "empty",
			input:    "",
			expected: []gitindex.Branch{},
		},
		{
			name:  "single",
			input: "HEAD",
			expected: []gitindex.Branch{
				{Name: "HEAD"},
			},
		},
		{
			name:  "single with prior commit",
			input: "deadbeef..HEAD",
			expected: []gitindex.Branch{
				{Name: "HEAD", PriorCommit: "deadbeef"},
			},
		},
		{
			name:  "single with empty prior commit",
			input: "..HEAD",
			expected: []gitindex.Branch{
				{Name: "HEAD"},
			},
		},
		{
			name:  "multiple with mixed prior commits",
			input: "HEAD,deadbeef..release,main",
			expected: []gitindex.Branch{
				{Name: "HEAD"},
				{Name: "main"},
				{Name: "release", PriorCommit: "deadbeef"},
			},
		},
		{
			name:    "too many parts",
			input:   "deadbeef..deadc0de..release",
			wantErr: true,
		},
		{
			name:    "empty branch name",
			input:   "deadc0de..",
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := parseBranchString(test.input)
			if test.wantErr {
				if err == nil {
					t.Error("expected an error, but didn't receive one")
				}

				return
			}

			if err != nil {
				t.Errorf("didn't expect an error, but got: %s", err)
			}

			sortBranches(t, actual)
			sortBranches(t, test.expected)

			if diff := cmp.Diff(actual, test.expected, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("unexexpected difference (-want +expected):\n%s", diff)
			}
		})
	}
}

func sortBranches(t *testing.T, branches []gitindex.Branch) {
	t.Helper()

	sort.Slice(branches, func(i, j int) bool {
		if branches[i].Name < branches[j].Name {
			return true
		}

		return branches[i].PriorCommit < branches[j].PriorCommit
	})
}
