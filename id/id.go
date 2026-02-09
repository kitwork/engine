// Package id provides a suite of high-performance, unique, and optionally sortable ID generators.
// It supports multiple formats including Base62, Base58, Base36, and pure numeric IDs.
package id

import (
	"crypto/rand"
	"fmt"
	"math/big"
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
	// Default Epoch set to 2025-01-01 00:00:00 UTC.
	// Used for generating sortable short IDs.
	since = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

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
// Supported lengths: 10, 26, 36, 58, 62.
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

// Repeat configures the generator to allow repeated characters (Standard Random).
// If false (default), it creates a permutation (Partial Shuffle, unique characters).
func (g *Generator) Repeat(repeat bool) *Generator {
	g.repeat = repeat
	return g
}

// Increasing configures the generator to produce time-based sortable IDs.
func (g *Generator) Increasing(increasing bool) *Generator {
	g.increasing = increasing
	return g
}

// --- Global Helpers ---

// Charset is a shortcut for New().Charset(n).
func Charset(n int) *Generator {
	return New().Charset(n)
}

// Entity returns a 36-char Base36 ID (e.g. Database Keys).
func Entity() string {
	return Charset(36).Must(36)
}

// Short returns a 6-char Base62 Random ID (e.g. Short Codes).
func Short() string {
	return Charset(62).Repeat(true).Must(6)
}

// Shortlink returns an 8-char Base58 ID (e.g. URLs).
func Shortlink() string {
	return Charset(58).Must(8)
}

// --- Execution Methods ---

// Must generates an ID of the specified length or panics on error.
// It automatically selects the strategy based on configuration:
// 1. Increasing=true -> Time-based Sortable.
// 2. Repeat=true     -> Random with Replacement.
// 3. Default         -> Random Permutation (No Replacement).
func (g *Generator) Must(length int) string {
	if g.err != nil {
		panic(g.err)
	}

	var res string
	var err error

	if g.increasing {
		res, err = g.Generate(length)
	} else if g.repeat {
		res = genRandomRepeated(length, g.charset)
	} else {
		res, err = g.Random(length)
	}

	if err != nil {
		panic(err)
	}
	return res
}

// Generate creates a Sortable ID.
func (g *Generator) Generate(lengths ...int) (string, error) {
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

	if length < 22 {
		return genShortAdaptive(length, cs, g.epoch), nil
	}

	return generate(cs, length), nil
}

// Random creates a Random ID (No Repeats).
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

	return genPureRandom(length, cs), nil
}

// --- Internal Engine ---

func genPureRandom(length int, charset []byte) string {
	l := len(charset)
	if length > l {
		length = l
	}

	avail := make([]byte, l)
	copy(avail, charset)

	for i := 0; i < length; i++ {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(l-i)))
		j := int(num.Int64()) + i
		avail[i], avail[j] = avail[j], avail[i]
	}

	return string(avail[:length])
}

func genRandomRepeated(length int, charset []byte) string {
	l := big.NewInt(int64(len(charset)))
	res := make([]byte, length)
	for i := 0; i < length; i++ {
		num, _ := rand.Int(rand.Reader, l)
		res[i] = charset[num.Int64()]
	}
	return string(res)
}

func genShortAdaptive(length int, charset []byte, instanceEpoch int64) string {
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
