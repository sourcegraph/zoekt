package main

import (
	"reflect"
	"testing"
)

func Test_RemoveBranch(t *testing.T) {
	var tests = []struct {
		name       string
		inBranch   string
		inBranches []string
		want       []string
	}{
		{
			name:       "branch at start",
			inBranch:   "main",
			inBranches: []string{"main", "a", "b"},
			want:       []string{"a", "b"},
		},
		{
			name:       "branch in middle",
			inBranch:   "main",
			inBranches: []string{"a", "main", "b"},
			want:       []string{"a", "b"},
		},
		{
			name:       "branch at end",
			inBranch:   "main",
			inBranches: []string{"a", "b", "main"},
			want:       []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeBranch(tt.inBranch, tt.inBranches)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Incorrect branch removal got %s, wanted %s", got, tt.want)
			}
		})
	}
}
