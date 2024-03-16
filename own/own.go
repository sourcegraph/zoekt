package own

import (
	"bytes"
	"fmt"
	"os"
)

func Load(ownPath string) (Own, error) {
	b, err := os.ReadFile(ownPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read owner file: %w", err)
	}

	owners := make(own)
	fields := bytes.Fields(b)
	for i := 0; i < len(fields); i += 2 {
		owners[string(fields[i])] = fields[i+1]
	}
	return owners, nil
}

var Empty = make(own)

type Own interface {
	// This is the way to go until we are proven it is too slow.
	CompileForAuthor(author string) func(path []byte) bool
}

type own map[string][]byte

func (o own) CompileForAuthor(author string) func(path []byte) bool {
	pattern, ok := o[author]
	if ok {
		return func(path []byte) bool {
			return bytes.Contains(path, pattern)
		}
	}
	return func(path []byte) bool {
		return false
	}
}
