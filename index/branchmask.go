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

// branchMask represents a variable-length bit mask for tracking which branches
// a file appears in. This allows supporting repositories with more than 64 branches.

// newBranchMask allocates a branch mask that can hold at least numBranches bits.
func newBranchMask(numBranches int) []byte {
	numBytes := (numBranches + 7) / 8
	if numBytes == 0 {
		numBytes = 1
	}
	return make([]byte, numBytes)
}

// setBit sets the bit at the given position in the mask.
func setBit(mask []byte, bit int) {
	byteIndex := bit / 8
	bitIndex := uint(bit % 8)
	if byteIndex < len(mask) {
		mask[byteIndex] |= 1 << bitIndex
	}
}

// getBit returns true if the bit at the given position is set.
func getBit(mask []byte, bit int) bool {
	byteIndex := bit / 8
	bitIndex := uint(bit % 8)
	if byteIndex >= len(mask) {
		return false
	}
	return (mask[byteIndex] & (1 << bitIndex)) != 0
}

// andMask performs a bitwise AND of two masks and returns the result.
// The result has the length of the shorter mask.
func andMask(a, b []byte) []byte {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	if minLen == 0 {
		return []byte{}
	}

	result := make([]byte, minLen)
	for i := 0; i < minLen; i++ {
		result[i] = a[i] & b[i]
	}
	return result
}

// orMask performs a bitwise OR of two masks and returns the result.
// The result has the length of the longer mask.
func orMask(a, b []byte) []byte {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return []byte{}
	}

	result := make([]byte, maxLen)
	copy(result, a)
	for i := 0; i < len(b); i++ {
		result[i] |= b[i]
	}
	return result
}

// orMaskInPlace performs a bitwise OR of b into a, modifying a in place.
// If a is shorter than b, this only ORs the overlapping bytes.
func orMaskInPlace(a, b []byte) {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		a[i] |= b[i]
	}
}

// isZero returns true if all bits in the mask are zero.
func isZero(mask []byte) bool {
	for _, b := range mask {
		if b != 0 {
			return false
		}
	}
	return true
}

// firstSetBit returns the index of the first set bit in the mask,
// or -1 if no bits are set.
func firstSetBit(mask []byte) int {
	for i, b := range mask {
		if b != 0 {
			for bit := 0; bit < 8; bit++ {
				if (b & (1 << uint(bit))) != 0 {
					return i*8 + bit
				}
			}
		}
	}
	return -1
}

// iterateBits calls fn for each set bit in the mask, passing the bit index.
func iterateBits(mask []byte, fn func(int)) {
	for i, b := range mask {
		if b == 0 {
			continue
		}
		for bit := 0; bit < 8; bit++ {
			if (b & (1 << uint(bit))) != 0 {
				fn(i*8 + bit)
			}
		}
	}
}

// copyMask creates a copy of the given mask.
func copyMask(mask []byte) []byte {
	if len(mask) == 0 {
		return []byte{}
	}
	result := make([]byte, len(mask))
	copy(result, mask)
	return result
}
