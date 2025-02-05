package index

import (
	"strings"
	"testing"
)

func TestShardName(t *testing.T) {
	tests := []struct {
		name     string
		indexDir string
		prefix   string
		version  int
		shardNum int
		expected string
	}{
		{
			name:     "short prefix",
			indexDir: "index",
			prefix:   "short",
			version:  1,
			shardNum: 42,
			expected: "index/short_v1.00042.zoekt",
		},
		{
			name:     "long prefix truncated",
			indexDir: "index",
			prefix:   strings.Repeat("a", 300),
			version:  2,
			shardNum: 1,
			expected: "index/" + strings.Repeat("a", 200) + "003ef1ba" + "_v2.00001.zoekt",
		},
		{
			name:     "empty indexDir",
			prefix:   "short",
			version:  1,
			expected: "short_v1.00000.zoekt",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := ShardName(test.indexDir, test.prefix, test.version, test.shardNum)
			if actual != test.expected {
				t.Errorf("expected %q, got %q", test.expected, actual)
			}
		})
	}
}

func TestDetermineLanguageIfUnknown(t *testing.T) {
	tests := []struct {
		name        string
		doc         Document
		wantLang    string
		skipContent bool
	}{
		{
			name: "already has language",
			doc: Document{
				Name:     "test.java",
				Language: "Go",
				Content:  []byte("package main"),
			},
			wantLang: "Go",
		},
		{
			name: "skipped file",
			doc: Document{
				Name:       "large.js",
				SkipReason: "too large",
				Content:    []byte(notIndexedMarker + "too large"),
			},
			wantLang: "JavaScript",
		},
		{
			name: "skipped file with unknown extension",
			doc: Document{
				Name:       "deadb33f",
				SkipReason: "binary",
				Content:    []byte(notIndexedMarker + "binary"),
			},
			wantLang: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			DetermineLanguageIfUnknown(&tt.doc)
			if tt.doc.Language != tt.wantLang {
				t.Errorf("DetermineLanguageIfUnknown() got language = %v, want %v", tt.doc.Language, tt.wantLang)
			}
		})
	}
}
