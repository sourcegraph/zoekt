package gitindex

// contentSlab reduces per-file heap allocations by sub-slicing from a
// shared buffer. Each returned slice has its capacity capped (3-index
// slice) so appending to one file's content cannot overwrite adjacent
// data. Files larger than the slab get their own allocation.
type contentSlab struct {
	buf []byte
	cap int
}

func newContentSlab(slabCap int) contentSlab {
	return contentSlab{
		buf: make([]byte, 0, slabCap),
		cap: slabCap,
	}
}

// alloc returns a byte slice of length n. The caller must write into it
// immediately (the bytes are uninitialized when sourced from the slab).
func (s *contentSlab) alloc(n int) []byte {
	if n > s.cap {
		return make([]byte, n)
	}
	if len(s.buf)+n > cap(s.buf) {
		s.buf = make([]byte, n, s.cap)
		return s.buf[:n:n]
	}
	off := len(s.buf)
	s.buf = s.buf[:off+n]
	return s.buf[off : off+n : off+n]
}
