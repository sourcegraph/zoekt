package query

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unsafe"

	"github.com/RoaringBitmap/roaring"
)

func branchesReposEncode(brs []BranchRepos) ([]byte, error) {
	var b bytes.Buffer
	var enc [binary.MaxVarintLen64]byte
	varint := func(n uint64) {
		m := binary.PutUvarint(enc[:], n)
		b.Write(enc[:m])
	}
	str := func(s string) {
		varint(uint64(len(s)))
		b.WriteString(s)
	}
	strSize := func(s string) uint64 {
		return uint64(binary.PutUvarint(enc[:], uint64(len(s))) + len(s))
	}

	// Calculate size
	size := uint64(1) // version
	size += uint64(binary.PutUvarint(enc[:], uint64(len(brs))))
	for _, br := range brs {
		size += strSize(br.Branch)
		idsSize := br.Repos.GetSerializedSizeInBytes()
		size += uint64(binary.PutUvarint(enc[:], idsSize))
		size += idsSize
	}

	b.Grow(int(size))

	// Version
	b.WriteByte(1)

	// Length
	varint(uint64(len(brs)))

	for _, br := range brs {
		str(br.Branch)
		l := br.Repos.GetSerializedSizeInBytes()
		varint(l)

		n, err := br.Repos.WriteTo(&b)
		if err != nil {
			return nil, err
		}

		if uint64(n) != l {
			return nil, io.ErrShortWrite
		}
	}

	return b.Bytes(), nil
}

func branchesReposDecode(b []byte) ([]BranchRepos, error) {
	// binaryReader returns strings pointing into b to avoid allocations. We
	// don't own b, so we create a copy of it.
	r := binaryReader{b: append(make([]byte, 0, len(b)), b...)}

	// Version
	if v := r.byt(); v != 1 {
		return nil, fmt.Errorf("unsupported BranchRepos encoding version %d", v)
	}

	l := r.uvarint() // Length
	brs := make([]BranchRepos, l)

	for i := 0; i < l; i++ {
		brs[i].Branch = r.str()
		brs[i].Repos = r.bitmap()
	}

	return brs, r.err
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
