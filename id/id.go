// Package id provides a suite of high-performance, unique, and optionally sortable ID generators.
// It supports multiple formats including Base62, Base58, Base36, and pure numeric IDs.
package id

import (
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"
)

// Generator encapsulates the logic and state for ID generation.
type Generator struct {
	epoch      int64
	charset    []byte
	err        error
	repeat     bool
	increasing bool
}

var (
	// Default Epoch set to 2025-12-16 08:35:00 UTC.
	// Used for generating sortable short IDs.
	since = time.Date(2025, 12, 16, 8, 35, 0, 0, time.UTC)

	// Predefined charsets for different ID flavors.
	digits = "0123456789"
	lower  = "abcdefghijklmnopqrstuvwxyz"
	upper  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

	// Base58 excludes ambiguous characters: 0, O, I, l
	base58 = "123456789abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ"

	charset10 = []byte(digits)
	charset26 = []byte(lower)
	charset36 = []byte(digits + lower)
	charset58 = []byte(base58)
	charset62 = []byte(digits + lower + upper)

	seq atomic.Uint32
)

// --- Fluent API Builders ---

// New returns a new Generator.
// If no arguments are provided, it uses the default epoch (2025-01-01).
// If a time argument is provided, it uses that as the epoch.
func New(t ...time.Time) *Generator {
	if len(t) == 0 {
		return &Generator{epoch: since.UnixMilli()}
	}
	if len(t) > 1 {
		return &Generator{err: fmt.Errorf("id: too many epochs provided")}
	}
	ts := t[0].UnixMilli()
	if ts < 0 {
		return &Generator{err: fmt.Errorf("id: epoch must be non-negative")}
	}
	if ts > time.Now().UnixMilli() {
		return &Generator{err: fmt.Errorf("id: epoch must be in the past")}
	}
	return &Generator{epoch: ts}
}

// Charset sets the charset for the generator based on length.
func (g *Generator) Charset(n int) *Generator {
	switch n {
	case 10:
		g.charset = charset10
	case 26:
		g.charset = charset26
	case 36:
		g.charset = charset36
	case 58:
		g.charset = charset58
	case 62:
		g.charset = charset62
	default:
		g.err = fmt.Errorf("id: unsupported charset length %d", n)
	}
	return g
}

// Repeat configures the generator to allow repeated characters.
func (g *Generator) Repeat(repeat bool) *Generator {
	g.repeat = repeat
	return g
}

// Increasing configures the generator to produce time-based sortable IDs.
func (g *Generator) Increasing(increasing bool) *Generator {
	g.increasing = increasing
	return g
}

// Custom sets a custom charset.
func (g *Generator) Custom(chars string) *Generator {
	if len(chars) < 2 {
		g.err = fmt.Errorf("id: charset length must be at least 2")
		return g
	}
	g.charset = []byte(chars)
	return g
}

// --- Global Helpers ---

func Charset(n int) *Generator       { return New().Charset(n) }
func Custom(chars string) *Generator { return New().Custom(chars) }

func Entity() string    { return Charset(36).Must(36) }
func Short() string     { return Charset(62).Repeat(true).Must(6) }
func Shortlink() string { return Charset(58).Must(8) }

// DecodeEntity parses a standard 36-character Entity ID and returns its generation time.
func DecodeEntity(idStr string) (time.Time, error) {
	if len(idStr) != 36 {
		return time.Time{}, fmt.Errorf("id: invalid length for Entity ID, expected 36")
	}

	charset := charset36
	charsetLen := len(charset)

	timeLen := 0
	capacity := uint64(1)
	for i := 0; i < charsetLen; i++ {
		multiplier := uint64(charsetLen - i)
		if capacity > ^uint64(0)/multiplier {
			break
		}
		capacity *= multiplier
		timeLen++
	}

	timeChars := idStr[:timeLen]
	avail := make([]byte, charsetLen)
	copy(avail, charset)

	idxs := make([]int, timeLen)
	activeLen := charsetLen
	for i := 0; i < timeLen; i++ {
		char := timeChars[i]
		idx := -1
		for j := 0; j < activeLen; j++ {
			if avail[j] == char {
				idx = j
				break
			}
		}
		if idx == -1 {
			return time.Time{}, fmt.Errorf("id: invalid character '%c' in ID", char)
		}
		idxs[i] = idx
		for j := idx; j < activeLen-1; j++ {
			avail[j] = avail[j+1]
		}
		activeLen--
	}

	currentT := uint64(0)
	startBase := uint64(charsetLen - (timeLen - 1))
	for i := 0; i < timeLen; i++ {
		base := startBase + uint64((timeLen-1)-i)
		currentT = currentT*base + uint64(idxs[i])
	}

	// Revert the 12-bit sequence mask to get the true nanosecond delta
	nanoDelta := currentT &^ 0xfff
	targetEpochNano := since.UnixMilli() * 1000000
	timestampNano := targetEpochNano + int64(nanoDelta)

	return time.Unix(0, timestampNano).UTC(), nil
}

// --- Execution Methods ---

// Must generates an ID of the specified length or panics on error.
func (g *Generator) Must(length int) string {
	res, err := g.Sortable(length)
	if err != nil {
		panic(err)
	}
	return res
}

// Sortable generates a unique, sortable (monotonically increasing) ID.
func (g *Generator) Sortable(lengths ...int) (string, error) {
	if g.err != nil {
		return "", g.err
	}

	length := 22
	if len(lengths) > 0 && lengths[0] > 0 {
		length = lengths[0]
	}

	cs := g.charset
	if len(cs) == 0 {
		cs = charset62
	}

	// Always use time-based sortable strategy for Generate.
	if length < 22 {
		return genShortAdaptive(length, cs, g.epoch)
	}

	return generate(cs, length, g.epoch)
}

// Random creates a Random ID (No Repeats by default).
func (g *Generator) Random(lengths ...int) (string, error) {
	if g.err != nil {
		return "", g.err
	}

	length := 22
	if len(lengths) > 0 && lengths[0] > 0 {
		length = lengths[0]
	}

	cs := g.charset
	if len(cs) == 0 {
		cs = charset62
	}

	if g.repeat {
		return genRandomRepeated(length, cs), nil
	}
	return genPureRandom(length, cs), nil
}

// --- Internal Engine ---

// genPureRandom generates an ID by shuffling the charset.
// Uses math/rand/v2 for lock-free, zero-alloc speed.
func genPureRandom(length int, charset []byte) string {
	l := len(charset)
	if length > l {
		length = l
	}

	avail := make([]byte, l)
	copy(avail, charset)

	// Partial Shuffle
	for i := 0; i < length; i++ {
		j := i + rand.N(l-i)
		avail[i], avail[j] = avail[j], avail[i]
	}

	return string(avail[:length])
}

// genRandomRepeated generates a random ID allowing repeating characters.
func genRandomRepeated(length int, charset []byte) string {
	l := len(charset)
	res := make([]byte, length)
	for i := 0; i < length; i++ {
		res[i] = charset[rand.N(l)]
	}
	return string(res)
}

// genShortAdaptive uses a custom epoch to generate sortable IDs.
func genShortAdaptive(length int, charset []byte, instanceEpoch int64) (string, error) {
	charsetLen := len(charset)
	avail := make([]byte, charsetLen)
	copy(avail, charset)

	now := time.Now().UnixMilli()
	targetEpoch := instanceEpoch
	if targetEpoch == 0 {
		targetEpoch = since.UnixMilli()
	}
	if now < targetEpoch {
		now = targetEpoch
	}

	t := uint64(now - targetEpoch)
	t = (t << 12) | uint64(seq.Add(1)&0xfff)

	timeLen := 0
	capacity := uint64(1)
	for i := 0; i < charsetLen; i++ {
		multiplier := uint64(charsetLen - i)
		if capacity > ^uint64(0)/multiplier { // Prevent overflow
			break
		}
		capacity *= multiplier
		timeLen++
	}

	if timeLen > length {
		return "", fmt.Errorf("id: length %d insufficient to encode timestamp (needed %d)", length, timeLen)
	}

	tsChars, activeLen := encodeTimePart(t, avail, timeLen, charsetLen)
	randomLen := length - timeLen

	shuffledAvail := shuffleRemaining(avail[:activeLen])
	if randomLen > len(shuffledAvail) {
		randomLen = len(shuffledAvail)
	}

	return string(tsChars) + string(shuffledAvail[:randomLen]), nil
}

// generate uses UnixNano to create a globally unique sortable ID.
func generate(charset []byte, totalLen int, instanceEpoch int64) (string, error) {
	charsetLen := len(charset)
	avail := make([]byte, charsetLen)
	copy(avail, charset)

	now := time.Now().UnixNano()
	targetEpoch := instanceEpoch
	if targetEpoch == 0 {
		targetEpoch = since.UnixMilli()
	}
	targetEpochNano := targetEpoch * 1000000

	if now < targetEpochNano {
		now = targetEpochNano
	}
	t := uint64(now - targetEpochNano)
	t = (t &^ 0xfff) | uint64(seq.Add(1)&0xfff)

	timeLen := 0
	capacity := uint64(1)
	for i := 0; i < charsetLen; i++ {
		multiplier := uint64(charsetLen - i)
		if capacity > ^uint64(0)/multiplier {
			break
		}
		capacity *= multiplier
		timeLen++
	}

	if timeLen > totalLen {
		return "", fmt.Errorf("id: length %d insufficient to encode timestamp (needed %d)", totalLen, timeLen)
	}

	tsChars, activeLen := encodeTimePart(t, avail, timeLen, charsetLen)
	randomLen := totalLen - timeLen

	shuffledAvail := shuffleRemaining(avail[:activeLen])
	if randomLen > len(shuffledAvail) {
		randomLen = len(shuffledAvail)
	}

	return string(tsChars) + string(shuffledAvail[:randomLen]), nil
}

// --- Helpers ---

// encodeTimePart extracts time encoding and returns the used characters and the new active length of avail.
func encodeTimePart(t uint64, avail []byte, timeLen int, charsetLen int) ([]byte, int) {
	var resArr [256]byte
	var idxArr [256]int
	
	res := resArr[:timeLen]
	idxs := idxArr[:timeLen]

	currentT := t
	startBase := uint64(charsetLen - (timeLen - 1))
	for i := timeLen - 1; i >= 0; i-- {
		base := startBase + uint64((timeLen-1)-i)
		idxs[i] = int(currentT % base)
		currentT /= base
	}

	activeLen := charsetLen
	for i, idx := range idxs {
		res[i] = avail[idx]
		// In-place byte shift (Zero-allocation instead of append/slicing)
		for j := idx; j < activeLen-1; j++ {
			avail[j] = avail[j+1]
		}
		activeLen--
	}
	return res, activeLen
}

func shuffleRemaining(avail []byte) []byte {
	limit := len(avail)
	for i := limit - 1; i > 0; i-- {
		j := rand.N(i + 1)
		avail[i], avail[j] = avail[j], avail[i]
	}
	return avail
}
