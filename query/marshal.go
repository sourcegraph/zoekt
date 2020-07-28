package query

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
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
// RepoBranches_Encode-8    2.21ms ± 3%    0.63ms ± 5%  -71.55%  (p=0.000 n=10+10)
// RepoBranches_Decode-8    4.08ms ± 5%    1.20ms ± 1%  -70.58%  (p=0.000 n=10+10)
//
// name                   old bytes      new bytes      delta
// RepoBranches_Encode-8     393kB ± 0%     344kB ± 0%  -12.48%  (p=0.000 n=10+10)
//
// name                   old alloc/op   new alloc/op   delta
// RepoBranches_Encode-8     726kB ± 0%     344kB ± 0%  -52.59%  (p=0.000 n=6+9)
// RepoBranches_Decode-8    2.31MB ± 0%    1.58MB ± 0%  -31.62%  (p=0.000 n=10+10)
//
// name                   old allocs/op  new allocs/op  delta
// RepoBranches_Encode-8     20.0k ± 0%      0.0k ± 0%  -99.99%  (p=0.000 n=10+10)
// RepoBranches_Decode-8     50.6k ± 0%     20.8k ± 0%  -58.94%  (p=0.000 n=10+10)

// repoBranchesEncode implements an efficient encoder for RepoBranches.
func repoBranchesEncode(repoBranches map[string][]string) ([]byte, error) {
	var b bytes.Buffer
	var enc [8]byte
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

	// Version
	b.WriteByte(1)

	// Length
	varint(len(repoBranches))

	// Calculate size
	size := 0
	for name, branches := range repoBranches {
		size += strSize(name) + 1
		if l := len(branches); l == 1 && branches[0] == "HEAD" {
			continue
		} else if l > 255 {
			return nil, fmt.Errorf("can't encode more than 255 branches: %d", l)
		}
		for _, branch := range branches {
			size += strSize(branch)
		}
	}
	b.Grow(size)

	for name, branches := range repoBranches {
		str(name)

		// Special case "HEAD"
		if len(branches) == 1 && branches[0] == "HEAD" {
			b.WriteByte(0)
			continue
		}

		// length of branches is 64 or less
		b.WriteByte(byte(len(branches)))
		for _, branch := range branches {
			str(branch)
		}
	}

	return b.Bytes(), nil
}

// repoBranchesDecode implements an efficient decoder for RepoBranches.
func repoBranchesDecode(b []byte) (map[string][]string, error) {
	r := binaryReader{b: b}

	// Version
	if v := r.byt(); v != 1 {
		return nil, fmt.Errorf("unsupported RepoBranches encoding version %d", v)
	}

	// Length
	l := r.uvarint()
	repoBranches := make(map[string][]string, l)

	for i := 0; i < l; i++ {
		name := r.str()

		branchesLen := int(r.byt())

		// Special case "HEAD"
		if branchesLen == 0 {
			repoBranches[name] = []string{"HEAD"}
			continue
		}

		branches := make([]string, branchesLen)
		for j := 0; j < branchesLen; j++ {
			branches[j] = r.str()
		}
		repoBranches[name] = branches
	}

	return repoBranches, r.err
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
	s := string(b.b[:l])
	b.b = b.b[l:]
	return s
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
