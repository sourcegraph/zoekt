// Copyright 2025 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package index

import (
	"testing"
)

func TestNewBranchMask(t *testing.T) {
	tests := []struct {
		numBranches int
		wantLen     int
	}{
		{0, 1},
		{1, 1},
		{8, 1},
		{9, 2},
		{64, 8},
		{65, 9},
		{128, 16},
	}

	for _, tt := range tests {
		mask := newBranchMask(tt.numBranches)
		if len(mask) != tt.wantLen {
			t.Errorf("newBranchMask(%d) = len %d, want %d", tt.numBranches, len(mask), tt.wantLen)
		}
	}
}

func TestSetAndGetBit(t *testing.T) {
	mask := newBranchMask(128)

	// Test setting and getting various bits
	bits := []int{0, 1, 7, 8, 15, 63, 64, 127}
	for _, bit := range bits {
		if getBit(mask, bit) {
			t.Errorf("bit %d should be unset initially", bit)
		}
		setBit(mask, bit)
		if !getBit(mask, bit) {
			t.Errorf("bit %d should be set after setBit", bit)
		}
	}

	// Test that other bits remain unset
	if getBit(mask, 2) || getBit(mask, 16) || getBit(mask, 100) {
		t.Error("unset bits should return false")
	}
}

func TestGetBitOutOfBounds(t *testing.T) {
	mask := newBranchMask(8)
	if getBit(mask, 100) {
		t.Error("out of bounds bit should return false")
	}
}

func TestAndMask(t *testing.T) {
	a := []byte{0b11110000, 0b10101010}
	b := []byte{0b11001100, 0b01010101}
	result := andMask(a, b)

	expected := []byte{0b11000000, 0b00000000}
	if len(result) != len(expected) {
		t.Fatalf("andMask result length = %d, want %d", len(result), len(expected))
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("andMask result[%d] = %08b, want %08b", i, result[i], expected[i])
		}
	}
}

func TestAndMaskDifferentLengths(t *testing.T) {
	a := []byte{0xFF, 0xFF, 0xFF}
	b := []byte{0xAA}
	result := andMask(a, b)

	if len(result) != 1 {
		t.Errorf("andMask with different lengths should return shorter length, got %d", len(result))
	}
	if result[0] != 0xAA {
		t.Errorf("andMask result = %02x, want AA", result[0])
	}
}

func TestOrMask(t *testing.T) {
	a := []byte{0b11110000, 0b10101010}
	b := []byte{0b11001100, 0b01010101}
	result := orMask(a, b)

	expected := []byte{0b11111100, 0b11111111}
	if len(result) != len(expected) {
		t.Fatalf("orMask result length = %d, want %d", len(result), len(expected))
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("orMask result[%d] = %08b, want %08b", i, result[i], expected[i])
		}
	}
}

func TestOrMaskDifferentLengths(t *testing.T) {
	a := []byte{0x11, 0x22, 0x33}
	b := []byte{0xAA}
	result := orMask(a, b)

	expected := []byte{0xBB, 0x22, 0x33}
	if len(result) != len(expected) {
		t.Errorf("orMask should return longer length, got %d want %d", len(result), len(expected))
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("orMask result[%d] = %02x, want %02x", i, result[i], expected[i])
		}
	}
}

func TestOrMaskInPlace(t *testing.T) {
	a := []byte{0b11110000, 0b10101010}
	b := []byte{0b11001100, 0b01010101}
	orMaskInPlace(a, b)

	expected := []byte{0b11111100, 0b11111111}
	for i := range expected {
		if a[i] != expected[i] {
			t.Errorf("orMaskInPlace result[%d] = %08b, want %08b", i, a[i], expected[i])
		}
	}
}

func TestIsZero(t *testing.T) {
	tests := []struct {
		name string
		mask []byte
		want bool
	}{
		{"empty", []byte{}, true},
		{"zero byte", []byte{0}, true},
		{"zero bytes", []byte{0, 0, 0}, true},
		{"one bit set", []byte{1}, false},
		{"middle bit set", []byte{0, 0x10, 0}, false},
		{"last bit set", []byte{0, 0, 0x80}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isZero(tt.mask); got != tt.want {
				t.Errorf("isZero(%v) = %v, want %v", tt.mask, got, tt.want)
			}
		})
	}
}

func TestFirstSetBit(t *testing.T) {
	tests := []struct {
		name string
		mask []byte
		want int
	}{
		{"empty", []byte{}, -1},
		{"zero", []byte{0, 0}, -1},
		{"first bit", []byte{0b00000001}, 0},
		{"second bit", []byte{0b00000010}, 1},
		{"eighth bit", []byte{0b10000000}, 7},
		{"ninth bit", []byte{0, 0b00000001}, 8},
		{"middle", []byte{0, 0, 0b00100000}, 21},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstSetBit(tt.mask); got != tt.want {
				t.Errorf("firstSetBit(%v) = %d, want %d", tt.mask, got, tt.want)
			}
		})
	}
}

func TestIterateBits(t *testing.T) {
	mask := []byte{0b10100001, 0b00000100}
	var collected []int
	iterateBits(mask, func(bit int) {
		collected = append(collected, bit)
	})

	expected := []int{0, 5, 7, 10}
	if len(collected) != len(expected) {
		t.Fatalf("iterateBits collected %d bits, want %d", len(collected), len(expected))
	}
	for i := range expected {
		if collected[i] != expected[i] {
			t.Errorf("iterateBits bit[%d] = %d, want %d", i, collected[i], expected[i])
		}
	}
}

func TestIterateBitsEmpty(t *testing.T) {
	mask := []byte{0, 0, 0}
	called := false
	iterateBits(mask, func(bit int) {
		called = true
	})
	if called {
		t.Error("iterateBits should not call function for empty mask")
	}
}

func TestCopyMask(t *testing.T) {
	original := []byte{0x12, 0x34, 0x56}
	copy := copyMask(original)

	// Check values match
	if len(copy) != len(original) {
		t.Fatalf("copy length = %d, want %d", len(copy), len(original))
	}
	for i := range original {
		if copy[i] != original[i] {
			t.Errorf("copy[%d] = %02x, want %02x", i, copy[i], original[i])
		}
	}

	// Modify copy and ensure original is unchanged
	copy[0] = 0xFF
	if original[0] == 0xFF {
		t.Error("modifying copy should not affect original")
	}
}

func TestCopyMaskEmpty(t *testing.T) {
	original := []byte{}
	copy := copyMask(original)
	if len(copy) != 0 {
		t.Errorf("copy of empty mask should be empty, got length %d", len(copy))
	}
}
