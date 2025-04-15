package web

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAddLineNumbers(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		lineNum  int
		isBefore bool
		want     []lineMatch
	}{
		{
			name:     "empty content",
			content:  "",
			lineNum:  10,
			isBefore: true,
			want:     nil,
		},
		{
			name:     "single line before",
			content:  "hello world",
			lineNum:  10,
			isBefore: true,
			want: []lineMatch{
				{LineNum: 9, Content: "hello world"},
			},
		},
		{
			name:     "single line after",
			content:  "hello world",
			lineNum:  10,
			isBefore: false,
			want: []lineMatch{
				{LineNum: 11, Content: "hello world"},
			},
		},
		{
			name:     "multiple lines before",
			content:  "first line\nsecond line\nthird line",
			lineNum:  10,
			isBefore: true,
			want: []lineMatch{
				{LineNum: 7, Content: "first line"},
				{LineNum: 8, Content: "second line"},
				{LineNum: 9, Content: "third line"},
			},
		},
		{
			name:     "multiple lines after",
			content:  "first line\nsecond line\nthird line",
			lineNum:  10,
			isBefore: false,
			want: []lineMatch{
				{LineNum: 11, Content: "first line"},
				{LineNum: 12, Content: "second line"},
				{LineNum: 13, Content: "third line"},
			},
		},
		{
			name:     "content with empty lines before",
			content:  "first line\n\nthird line",
			lineNum:  10,
			isBefore: true,
			want: []lineMatch{
				{LineNum: 7, Content: "first line"},
				{LineNum: 8, Content: ""},
				{LineNum: 9, Content: "third line"},
			},
		},
		{
			name:     "content with empty lines after",
			content:  "first line\n\nthird line",
			lineNum:  10,
			isBefore: false,
			want: []lineMatch{
				{LineNum: 11, Content: "first line"},
				{LineNum: 12, Content: ""},
				{LineNum: 13, Content: "third line"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addLineNumbers(tt.content, tt.lineNum, tt.isBefore)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("addLineNumbers() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
