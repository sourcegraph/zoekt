package zoekt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"github.com/RoaringBitmap/roaring"
)

// Wire-format of map[uint32]*MinimalRepoListEntry is pretty straightforward:
//
// byte(1) version
// uvarint(len(minimal))
// for repoID, entry in minimal:
//   uvarint(repoID)
//   byte(entry.HasSymbols)
//   uvarint(len(entry.Branches))
//   for b in entry.Branches:
//     str(b.Name)
//     str(b.Version)

// stringSetEncode implements an efficient encoder for map[string]struct{}.
func stringSetEncode(minimal map[uint32]MinimalRepoListEntry) ([]byte, error) {
	var b bytes.Buffer
	var enc [binary.MaxVarintLen64]byte
	varint := func(n int) {
		m := binary.PutUvarint(enc[:], uint64(n))
		b.Write(enc[:m])
	}
	str := func(s string) {
		varint(len(s))
		b.WriteString(s)
	}
	strSize := func(s string) int {
		return binary.PutUvarint(enc[:], uint64(len(s))) + len(s)
	}

	// Calculate size
	size := 1 // version
	size += binary.PutUvarint(enc[:], uint64(len(minimal)))
	for repoID, entry := range minimal {
		size += binary.PutUvarint(enc[:], uint64(repoID))
		size += 1 // HasSymbols
		size += binary.PutUvarint(enc[:], uint64(len(entry.Branches)))
		for _, b := range entry.Branches {
			size += strSize(b.Name)
			size += strSize(b.Version)
		}
	}
	b.Grow(size)

	// Version
	b.WriteByte(1)

	// Length
	varint(len(minimal))

	for repoID, entry := range minimal {
		varint(int(repoID))

		hasSymbols := byte(1)
		if !entry.HasSymbols {
			hasSymbols = 0
		}
		b.WriteByte(hasSymbols)

		varint(len(entry.Branches))
		for _, b := range entry.Branches {
			str(b.Name)
			str(b.Version)
		}
	}

	return b.Bytes(), nil
}

// stringSetDecode implements an efficient decoder for map[string]struct{}.
func stringSetDecode(b []byte) (map[uint32]MinimalRepoListEntry, error) {
	// binaryReader returns strings pointing into b to avoid allocations. We
	// don't own b, so we create a copy of it.
	r := binaryReader{b: append([]byte{}, b...)}

	// Version
	if v := r.byt(); v != 1 {
		return nil, fmt.Errorf("unsupported stringSet encoding version %d", v)
	}

	// Length
	l := r.uvarint()
	m := make(map[uint32]MinimalRepoListEntry, l)
	allBranches := make([]RepositoryBranch, 0, l)

	for i := 0; i < l; i++ {
		repoID := r.uvarint()
		hasSymbols := r.byt() == 1
		lb := r.uvarint()
		for i := 0; i < lb; i++ {
			allBranches = append(allBranches, RepositoryBranch{
				Name:    r.str(),
				Version: r.str(),
			})
		}
		branches := allBranches[len(allBranches)-lb:]
		m[uint32(repoID)] = MinimalRepoListEntry{
			HasSymbols: hasSymbols,
			Branches:   branches,
		}
	}

	return m, r.err
}

type binaryReader struct {
	b   []byte
	err error
}

func (b *binaryReader) uvarint() int {
	x, n := binary.Uvarint(b.b)
	if n < 0 {
		b.b = nil
		b.err = errors.New("malformed RepoBranches")
		return 0
	}
	b.b = b.b[n:]
	return int(x)
}

func (b *binaryReader) str() string {
	l := b.uvarint()
	if l > len(b.b) {
		b.b = nil
		b.err = errors.New("malformed RepoBranches")
		return ""
	}
	s := b2s(b.b[:l])
	b.b = b.b[l:]
	return s
}

func (b *binaryReader) bitmap() *roaring.Bitmap {
	l := b.uvarint()
	if l > len(b.b) {
		b.b = nil
		b.err = errors.New("malformed BranchRepos")
		return nil
	}
	r := roaring.New()
	_, b.err = r.FromBuffer(b.b[:l])
	b.b = b.b[l:]
	return r
}

func (b *binaryReader) byt() byte {
	if len(b.b) < 1 {
		b.b = nil
		b.err = errors.New("malformed RepoBranches")
		return 0
	}
	x := b.b[0]
	b.b = b.b[1:]
	return x
}

func b2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
