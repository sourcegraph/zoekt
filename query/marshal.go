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

// We implement a custom binary marshaller for a list of repos to
// branches. When profiling Sourcegraph this is one of the dominant items.
//
// Wire-format of map[string][]string is pretty straightforward:
//
// byte(1) version
// uvarint(len(map))
// for k, vs in map:
//   str(k)
//   uvarint(len(vs))
//   for v in vs:
//     str(v)
//
//  where str(v) is uvarint(len(v)) bytes(v)
//
// The above format gives about the same size encoding as gob does. However,
// gob doesn't have a specialization for map[string][]string so we get to
// avoid a lot of intermediate allocations.
//
// The only other specialization we add is treating []string{"HEAD"} as if it
// was []string{}. This is the most common value for branches so avoids the
// need to write it on the wire. This makes us beat gob for encoded size.
//
// The above adds up to a huge improvement, worth the extra complexity:
//
// name                   old time/op    new time/op    delta
// RepoBranches_Encode-8    2.37ms ± 3%    0.62ms ± 0%   -73.77%  (p=0.000 n=10+8)
// RepoBranches_Decode-8    4.19ms ± 2%    0.74ms ± 1%   -82.37%  (p=0.000 n=10+9)
//
// name                   old bytes      new bytes      delta
// RepoBranches_Encode-8     393kB ± 0%     344kB ± 0%   -12.48%  (p=0.000 n=10+10)
//
// name                   old alloc/op   new alloc/op   delta
// RepoBranches_Encode-8     726kB ± 0%     344kB ± 0%   -52.60%  (p=0.000 n=10+9)
// RepoBranches_Decode-8    2.31MB ± 0%    1.44MB ± 0%   -37.51%  (p=0.000 n=9+10)
//
// name                   old allocs/op  new allocs/op  delta
// RepoBranches_Encode-8     20.0k ± 0%      0.0k ± 0%  -100.00%  (p=0.000 n=10+10)
// RepoBranches_Decode-8     50.6k ± 0%      0.4k ± 0%   -99.26%  (p=0.000 n=10+10)

func (rb *RepoBranches) MarshalBinary() ([]byte, error) {
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

	switch {
	case len(rb.IDs) > 0:
		// Calculate size
		size := uint64(1) // version
		size += uint64(binary.PutUvarint(enc[:], uint64(len(rb.IDs))))
		for branch, ids := range rb.IDs {
			size += strSize(branch)
			idsSize := ids.GetSerializedSizeInBytes()
			size += uint64(binary.PutUvarint(enc[:], idsSize))
			size += idsSize
		}

		b.Grow(int(size))

		// Version
		b.WriteByte(2)

		// Length
		varint(uint64(len(rb.IDs)))

		for branch, ids := range rb.IDs {
			str(branch)
			ids.RunOptimize()
			l := ids.GetSerializedSizeInBytes()
			varint(l)

			n, err := ids.WriteTo(&b)
			if err != nil {
				return nil, err
			}

			if uint64(n) != l {
				return nil, io.ErrShortWrite
			}
		}

	case len(rb.Set) > 0:
		// Calculate size
		size := uint64(1) // version
		size += uint64(binary.PutUvarint(enc[:], uint64(len(rb.Set))))
		for name, branches := range rb.Set {
			size += strSize(name) + 1
			if l := len(branches); l == 1 && branches[0] == "HEAD" {
				continue
			} else if l == 0 {
				// We reserve "0" for the "HEAD" special case.
				return nil, fmt.Errorf("repo with no branches: %q", name)
			} else if l > 255 {
				// We encode branches len as a byte (saves 11% cpu vs varint). This is
				// fine sinze Zoekt can only index upto 64 branches (uses a bitmask on a
				// 64bit int to encode branch information for a document)
				return nil, fmt.Errorf("can't encode more than 255 branches: %d", l)
			}
			for _, branch := range branches {
				size += strSize(branch)
			}
		}

		b.Grow(int(size))

		// Version
		b.WriteByte(1)

		// Length
		varint(uint64(len(rb.Set)))

		for name, branches := range rb.Set {
			str(name)

			// Special case "HEAD"
			if len(branches) == 1 && branches[0] == "HEAD" {
				branches = nil
			}

			b.WriteByte(byte(len(branches)))
			for _, branch := range branches {
				str(branch)
			}
		}
	}

	return b.Bytes(), nil
}

// head is the most common slice of branches we search. We re-use it to avoid
// allocations when decoding. We know that zoekt never mutates the
// repoBranches slice, so it is safe to share this slice.
var head = []string{"HEAD"}

func (rb *RepoBranches) UnmarshalBinary(b []byte) error {
	// binaryReader returns strings pointing into b to avoid allocations. We
	// don't own b, so we create a copy of it.
	r := binaryReader{b: append([]byte{}, b...)}

	switch v := r.byt(); v { // Version
	case 1:
		// Length
		l := r.uvarint()
		repoBranches := make(map[string][]string, l)

		for i := 0; i < l; i++ {
			name := r.str()

			branchesLen := int(r.byt())

			// Special case "HEAD"
			if branchesLen == 0 {
				repoBranches[name] = head
				continue
			}

			branches := make([]string, branchesLen)
			for j := 0; j < branchesLen; j++ {
				branches[j] = r.str()
			}
			repoBranches[name] = branches
		}

		rb.Set = repoBranches

		return r.err
	case 2:
		// Length
		l := r.uvarint()
		branchIDs := make(map[string]*roaring.Bitmap, l)

		for i := 0; i < l; i++ {
			branch := r.str()
			branchIDs[branch] = r.bitmap()
		}

		rb.IDs = branchIDs

		return r.err
	default:
		return fmt.Errorf("unsupported RepoBranches encoding version %d", v)
	}
}

type binaryReader struct {
	b   []byte
	off int
	err error
}

func (b *binaryReader) uvarint() int {
	x, n := binary.Uvarint(b.b[b.off:])
	if n < 0 {
		b.b = nil
		b.err = errors.New("malformed RepoBranches")
		return 0
	}
	b.off += n
	return int(x)
}

func (b *binaryReader) str() string {
	l := b.uvarint()
	if l > len(b.b[b.off:]) {
		b.b = nil
		b.err = errors.New("malformed RepoBranches")
		return ""
	}
	s := b2s(b.b[b.off : b.off+l])
	b.off += l
	return s
}

func (b *binaryReader) bitmap() *roaring.Bitmap {
	l := b.uvarint()
	if l > len(b.b[b.off:]) {
		b.b = nil
		b.err = errors.New("malformed RepoBranches")
		return nil
	}
	r := roaring.New()
	_, b.err = r.FromBuffer(b.b[b.off : b.off+l])
	b.off += l
	return r
}

func (b *binaryReader) byt() byte {
	if len(b.b[b.off:]) < 1 {
		b.b = nil
		b.err = errors.New("malformed RepoBranches")
		return 0
	}
	x := b.b[b.off]
	b.off++
	return x
}

func b2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
