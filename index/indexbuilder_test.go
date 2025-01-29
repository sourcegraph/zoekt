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
