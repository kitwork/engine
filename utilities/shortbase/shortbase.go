// Package shortbase is a reversible base-N transcoder: re-express a value written in one alphabet
// (`from`) as a shorter string in another (`to`), and back, EXACTLY.
//
// The `from` alphabet's characters ARE its digits, so it may include separators like "." — meaning the
// input can be any structured token (a dotted decimal, "post.comment", "1.2.3"), not only integers.
// big.Int inside, so the value can be arbitrarily long.
//
// prefix / suffix are fixed literal markers wrapped around the body; their characters are RESERVED
// out of the body alphabet so the marker stays cleanly separable and reversible.
//
// It is NOT encryption or a hash: fully reversible, and the codes stay ordered/guessable.
//
// This is pure Go (strings in, strings out). The kitwork() JS binding lives in work/shortbase.go.
package shortbase

import (
	"math/big"
	"strings"
)

// Common alphabets. ORDER is significant — it fixes the digit positions, so never reorder a live one.
// The digit block is "1234567890" (1–9 then 0), matching the codes already generated in this project.
const (
	Digits  = "0123456789"                 // base 10 — 0–9
	Numeric = "1234567890"                 // base 10 — 1–9 then 0 (existing order)
	Lower   = "abcdefghijklmnopqrstuvwxyz" // 26
	Upper   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ" // 26

	Alpha  = Upper + Lower           // base 52 — A–Z a–z
	Base11 = Digits + "."            // base 11 — dotted decimal
	Base36 = Numeric + Lower         // base 36 — 1234567890 a–z
	Base62 = Numeric + Upper + Lower // base 62 — 1234567890 A–Z a–z

	Base39 = "0123456789abcdefghijklmnopqrstuvwxyz._-"
	// Base58 drops 0 O I l — glyphs confused when reading or typing a code.
	Base58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
)

// Codec is one configured (from, to, prefix, suffix). Immutable: the builder methods return a copy.
type Codec struct {
	from, to, prefix, suffix string
}

// Default codec: dotted decimal (base11) → base62.
func Default() *Codec { return &Codec{from: Base11, to: Base62} }

func (c *Codec) with(from, to, prefix, suffix string) *Codec {
	return &Codec{from: from, to: to, prefix: prefix, suffix: suffix}
}

// From sets the INPUT alphabet (needs ≥ 2 distinct characters; an invalid one is ignored).
func (c *Codec) From(a string) *Codec {
	if len([]rune(a)) >= 2 {
		return c.with(a, c.to, c.prefix, c.suffix)
	}
	return c
}

// To sets the OUTPUT alphabet (needs ≥ 2 distinct characters).
func (c *Codec) To(a string) *Codec {
	if len([]rune(a)) >= 2 {
		return c.with(c.from, a, c.prefix, c.suffix)
	}
	return c
}

// Prefix / Suffix set fixed literal markers wrapped around the body.
func (c *Codec) Prefix(p string) *Codec { return c.with(c.from, c.to, p, c.suffix) }
func (c *Codec) Suffix(s string) *Codec { return c.with(c.from, c.to, c.prefix, s) }

// body is `to` minus every prefix/suffix character, so the body can never collide with a marker.
func (c *Codec) body() string { return remove(c.to, c.prefix+c.suffix) }

// Encode re-expresses value (written in `from`) as prefix+body+suffix. ok=false if value holds a
// character outside `from`, or the body alphabet collapsed below 2 characters.
func (c *Codec) Encode(value string) (string, bool) {
	body := c.body()
	if len([]rune(body)) < 2 {
		return "", false
	}
	n, ok := toInt(value, c.from)
	if !ok {
		return "", false
	}
	return c.prefix + fromInt(n, body) + c.suffix, true
}

// Decode reverses Encode: strip the markers, read the core in the body alphabet, render back in
// `from`. ok=false if the core holds a character outside the body alphabet.
func (c *Codec) Decode(code string) (string, bool) {
	body := c.body()
	if len([]rune(body)) < 2 {
		return "", false
	}
	core := code
	if c.prefix != "" {
		core = strings.TrimPrefix(core, c.prefix)
	}
	if c.suffix != "" {
		core = strings.TrimSuffix(core, c.suffix)
	}
	n, ok := toInt(core, body)
	if !ok {
		return "", false
	}
	return fromInt(n, c.from), true
}

// toInt reads s as a positional number in `alphabet` (base = len). ok=false on an empty string or any
// character not in the alphabet. Leading alphabet[0] characters carry no value (like leading zeros).
func toInt(s, alphabet string) (*big.Int, bool) {
	if s == "" {
		return nil, false
	}
	chars := []rune(alphabet)
	base := big.NewInt(int64(len(chars)))
	result := big.NewInt(0)
	for _, c := range s {
		idx := indexRune(chars, c)
		if idx < 0 {
			return nil, false
		}
		result.Mul(result, base)
		result.Add(result, big.NewInt(int64(idx)))
	}
	return result, true
}

// fromInt renders n in `alphabet`, most-significant digit first, on a copy of n.
func fromInt(n *big.Int, alphabet string) string {
	chars := []rune(alphabet)
	base := big.NewInt(int64(len(chars)))
	zero := big.NewInt(0)
	if n.Cmp(zero) == 0 {
		return string(chars[0])
	}
	m := new(big.Int).Set(n)
	mod := new(big.Int)
	var out []rune
	for m.Cmp(zero) > 0 {
		m.DivMod(m, base, mod)
		out = append(out, chars[mod.Int64()])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// remove returns alphabet with every character in drop deleted.
func remove(alphabet, drop string) string {
	if drop == "" {
		return alphabet
	}
	set := make(map[rune]struct{}, len(drop))
	for _, c := range drop {
		set[c] = struct{}{}
	}
	var out []rune
	for _, c := range alphabet {
		if _, skip := set[c]; !skip {
			out = append(out, c)
		}
	}
	return string(out)
}

func indexRune(chars []rune, target rune) int {
	for i, c := range chars {
		if c == target {
			return i
		}
	}
	return -1
}
