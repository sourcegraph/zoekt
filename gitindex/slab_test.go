package gitindex

import "testing"

func TestContentSlab(t *testing.T) {
	t.Run("fits in slab", func(t *testing.T) {
		s := newContentSlab(1024)
		b := s.alloc(100)
		if len(b) != 100 {
			t.Fatalf("len = %d, want 100", len(b))
		}
		if cap(b) != 100 {
			t.Fatalf("cap = %d, want 100 (3-index slice)", cap(b))
		}
	})

	t.Run("cap is capped so append cannot corrupt adjacent data", func(t *testing.T) {
		s := newContentSlab(1024)
		a := s.alloc(10)
		copy(a, []byte("aaaaaaaaaa"))

		b := s.alloc(10)
		copy(b, []byte("bbbbbbbbbb"))

		// Appending to a must not overwrite b.
		a = append(a, 'X') // triggers new backing array since cap==len
		if string(b) != "bbbbbbbbbb" {
			t.Fatalf("adjacent data corrupted: got %q", b)
		}
		_ = a
	})

	t.Run("slab rollover", func(t *testing.T) {
		s := newContentSlab(64)
		a := s.alloc(60)
		if len(a) != 60 || cap(a) != 60 {
			t.Fatalf("a: len=%d cap=%d", len(a), cap(a))
		}
		// Next alloc doesn't fit in remaining 4 bytes → new slab.
		b := s.alloc(10)
		if len(b) != 10 || cap(b) != 10 {
			t.Fatalf("b: len=%d cap=%d", len(b), cap(b))
		}
		// a and b should not share backing arrays.
		copy(a, make([]byte, 60))
		copy(b, []byte("0123456789"))
		if string(b) != "0123456789" {
			t.Fatal("rollover corrupted data")
		}
	})

	t.Run("oversized allocation", func(t *testing.T) {
		s := newContentSlab(64)
		b := s.alloc(128)
		if len(b) != 128 {
			t.Fatalf("len = %d, want 128", len(b))
		}
		// Oversized alloc should not consume slab space.
		c := s.alloc(32)
		if len(c) != 32 || cap(c) != 32 {
			t.Fatalf("c: len=%d cap=%d", len(c), cap(c))
		}
	})

	t.Run("zero size", func(t *testing.T) {
		s := newContentSlab(64)
		b := s.alloc(0)
		if len(b) != 0 {
			t.Fatalf("len = %d, want 0", len(b))
		}
	})
}
