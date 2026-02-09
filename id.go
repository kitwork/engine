package engine

import (
	"crypto/rand"
	"math/big"
	"time"
)

// KitworkID creates a 36-character Kitwork ID.
// Format: [timestamp_permutation][random_permutation]
// GUARANTEE: STRICTLY UNIQUE CHARACTERS across the entire string.
// No character repeats. Ever. (Permutation of 36 chars)
// Monotonicity: Preserved by Mixed Radix (Factorial-like) Encoding of Timestamp.
func Gen() string {
	// 01. Prepare Full Charset
	// We operate on a shrinking charset to ensure global uniqueness within the ID.
	originalCharset := []byte("0123456789abcdefghijklmnopqrstuvwxyz")

	// Copy to available slice
	avail := make([]byte, len(originalCharset))
	copy(avail, originalCharset)

	// 02. Time Part (Unique Monotonic Encoding)
	// We encode time into the first 13 characters using a Mixed Radix system.
	// This ensures:
	// 1. Monotonicity: The string sorts chronologically.
	// 2. Uniqueness: Used characters are removed from 'avail', so they don't repeat.

	t := uint64(time.Now().UnixNano())

	// Randomize the last 2 decimal digits (00-99) to add entropy
	// (Useful if system clock precision is low, e.g. ends in 00s)
	jitter, _ := rand.Int(rand.Reader, big.NewInt(100))
	t = (t / 100 * 100) + uint64(jitter.Int64())

	// We need 13 digits for time.
	// The bases for these digits (from LSB to MSB) will be:
	// Pos 12 (LSB): Base 24 (Since 36 - 12 = 24 chars left)
	// Pos 11: Base 25
	// ...
	// Pos 0 (MSB): Base 36

	idxs := make([]int, 13)
	currentT := t
	startBase := uint64(24) // Base for the last timestamp character (index 12)

	// Extract digits from LSB (Right) to MSB (Left)
	for i := 12; i >= 0; i-- {
		base := startBase + uint64(12-i) // Base increases as we go left: 24, 25... 36
		idxs[i] = int(currentT % base)
		currentT /= base
	}

	var res []byte

	// 03. Construct Time Part Strings
	// Apply digits to 'avail' charset to pick characters.
	for _, idx := range idxs {
		// Pick char at index
		char := avail[idx]
		res = append(res, char)

		// Remove char from avail to ensure no repeats
		avail = append(avail[:idx], avail[idx+1:]...)
	}

	// 04. Random Padding (Unique Shuffle)
	// We have 23 characters remaining in 'avail'.
	// Just shuffle them and append to fill the rest of the 36-char ID.

	// Fisher-Yates Shuffle on remaining available chars
	limit := len(avail)
	for i := limit - 1; i > 0; i-- {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		j := num.Int64()
		avail[i], avail[j] = avail[j], avail[i]
	}

	// Append all remaining shuffled chars
	res = append(res, avail...)

	return string(res)
}
