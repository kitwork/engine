// Package id provides a suite of high-performance, unique, and optionally sortable ID generators.
// It supports multiple formats including Base62, Base58, Base36, and pure numeric IDs.
package id

import (
	"crypto/rand"
	"math/big"
	"time"
)

var (
	// Default Epoch set to 2025-01-01 00:00:00 UTC.
	// Used for generating sortable short IDs.
	epoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

	// Predefined charsets for different ID flavors.
	charset10 = []byte("0123456789")
	charset26 = []byte("abcdefghijklmnopqrstuvwxyz")
	charset36 = []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	charset58 = []byte("123456789abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ")
	charset62 = []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
)

// SetEpoch updates the starting point for short ID generation.
// This allows for shorter IDs by storing time as an offset from this date.
func SetEpoch(t time.Time) {
	epoch = t.UnixMilli()
}

// --- Smart Discovery API ---

// Gen is the main entry point for the package.
// Calling Gen() without arguments returns a standard 36-character Base36 sortable ID.
// Calling Gen(length) returns an ID of the specified length with the best strategy:
//   - length <= 8: High-entropy Pure Random ID (to prevent collisions in tight spaces).
//   - 8 < length < 22: Time-based sortable ID using custom Epoch (max efficiency).
//   - length >= 22: Time-based sortable ID using UnixNano (Globally unique).
func Gen(lengths ...int) string {
	if len(lengths) == 0 {
		return generate(charset36, 36)
	}

	length := lengths[0]

	// High-traffic collision prevention for small lengths.
	if length <= 8 {
		return genPureRandom(length, charset62)
	}

	// Mid-range: Sortable but efficient.
	if length < 22 {
		return genShortAdaptive(length, charset62)
	}

	// Long-range: Globally unique using high-precision timestamp.
	return generate(charset62, length)
}

// Generate is an alias for Gen() to provide a standard naming convention.
func Generate() string {
	return Gen()
}

// --- Specific Purpose API ---

// Gen10 returns a 10-character numeric-only ID.
func Gen10() string { return generate(charset10, 10) }

// Gen26 returns a 26-character lowercase alphabet ID.
func Gen26() string { return generate(charset26, 26) }

// Gen36 returns a 36-character alphanumeric ID (lowercase + digits).
func Gen36() string { return generate(charset36, 36) }

// Gen62 returns a 22-character high-density alphanumeric ID.
func Gen62() string { return generate(charset62, 22) }

// Gen58 returns a 22-character human-friendly ID (excludes 0, O, I, l).
func Gen58() string { return generate(charset58, 22) }

// GenShort returns a Base62 ID of the given length using adaptive time strategy.
func GenShort(l int) string { return Gen(l) }

// Gen6 returns a 6-character high-entropy Base62 ID.
func Gen6() string { return Gen(6) }

// Gen8 returns an 8-character high-entropy Base62 ID.
func Gen8() string { return Gen(8) }

// Gen6_58 returns a 6-character human-friendly Base58 ID.
func Gen6_58() string { return genPureRandom(6, charset58) }

// Gen8_58 returns an 8-character human-friendly Base58 ID.
func Gen8_58() string { return genPureRandom(8, charset58) }

// --- Internal Engine ---

// genPureRandom generates a high-entropy ID by selecting random characters from the charset.
// Suitable for small lengths where time-encoding would lead to collisions.
func genPureRandom(length int, charset []byte) string {
	res := make([]byte, length)
	limit := big.NewInt(int64(len(charset)))
	for i := 0; i < length; i++ {
		num, _ := rand.Int(rand.Reader, limit)
		res[i] = charset[num.Int64()]
	}
	return string(res)
}

// genShortAdaptive uses a custom epoch to encode milliseconds into the ID.
func genShortAdaptive(length int, charset []byte) string {
	charsetLen := len(charset)
	avail := make([]byte, charsetLen)
	copy(avail, charset)

	now := time.Now().UnixMilli()
	if now < epoch {
		now = epoch
	}
	t := uint64(now - epoch)

	// Add 2 digits of jitter for millisecond collisions.
	jitter, _ := rand.Int(rand.Reader, big.NewInt(100))
	t = (t * 100) + uint64(jitter.Int64())

	timeLen := 0
	capacity := new(big.Int).SetUint64(1)
	tBig := new(big.Int).SetUint64(t)
	for i := 0; i < charsetLen; i++ {
		capacity.Mul(capacity, big.NewInt(int64(charsetLen-i)))
		timeLen++
		if capacity.Cmp(tBig) > 0 {
			break
		}
	}

	if timeLen > length {
		timeLen = length
	}
	tsChars := encodeTimePart(t, &avail, timeLen, charsetLen)

	randomLen := length - timeLen
	shuffledAvail := shuffleRemaining(avail)
	if randomLen > len(shuffledAvail) {
		randomLen = len(shuffledAvail)
	}
	return string(tsChars) + string(shuffledAvail[:randomLen])
}

// generate uses UnixNano to create a globally unique sortable ID.
func generate(charset []byte, totalLen int) string {
	charsetLen := len(charset)
	avail := make([]byte, charsetLen)
	copy(avail, charset)

	t := uint64(time.Now().UnixNano())
	jitter, _ := rand.Int(rand.Reader, big.NewInt(100))
	t = (t / 100 * 100) + uint64(jitter.Int64())

	timeLen := 0
	capacity := new(big.Int).SetUint64(1)
	tBig := new(big.Int).SetUint64(t)
	for i := 0; i < charsetLen; i++ {
		capacity.Mul(capacity, big.NewInt(int64(charsetLen-i)))
		timeLen++
		if capacity.Cmp(tBig) > 0 {
			break
		}
	}

	if timeLen > totalLen {
		timeLen = totalLen
	}
	tsChars := encodeTimePart(t, &avail, timeLen, charsetLen)

	randomLen := totalLen - timeLen
	shuffledAvail := shuffleRemaining(avail)
	if randomLen > len(shuffledAvail) {
		randomLen = len(shuffledAvail)
	}
	return string(tsChars) + string(shuffledAvail[:randomLen])
}

// --- Helpers ---

func encodeTimePart(t uint64, avail *[]byte, timeLen int, charsetLen int) []byte {
	idxs := make([]int, timeLen)
	currentT := t
	startBase := uint64(charsetLen - (timeLen - 1))
	for i := timeLen - 1; i >= 0; i-- {
		base := startBase + uint64((timeLen-1)-i)
		idxs[i] = int(currentT % base)
		currentT /= base
	}
	var res []byte
	for _, idx := range idxs {
		char := (*avail)[idx]
		res = append(res, char)
		*avail = append((*avail)[:idx], (*avail)[idx+1:]...)
	}
	return res
}

func shuffleRemaining(avail []byte) []byte {
	limit := len(avail)
	for i := limit - 1; i > 0; i-- {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		j := num.Int64()
		avail[i], avail[j] = avail[j], avail[i]
	}
	return avail
}
