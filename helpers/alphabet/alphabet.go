// Package alphabet holds the character sets ("alphabets") used as bases — by the shortbase codec, and
// anywhere a base-N alphabet is handy.
//
// For a codec, an alphabet's LENGTH is the base and its character ORDER fixes the digit positions:
// change the order and every code changes (and old codes stop decoding). So treat these as STABLE,
// versioned constants once any code is in the wild — never reorder a live one.
//
// The digit block is "1234567890" (1–9 then 0), matching the codes already generated in this project.
package alphabet

const (
	Numeric = "1234567890"                 // base 10 — 1–9 then 0 (existing order, NOT 0–9)
	Lower   = "abcdefghijklmnopqrstuvwxyz" // 26
	Upper   = "ABCDEFGHIJKLMNOPQRSTUVWXYZ" // 26

	Alpha  = Upper + Lower           // base 52 — A–Z then a–z
	Base36 = Numeric + Lower         // base 36 — 1234567890 a–z
	Base62 = Numeric + Upper + Lower // base 62 — 1234567890 A–Z a–z

	// Base58 drops 0 O I l — the glyphs people confuse reading or typing a code. shortbase's default.
	// (It has no 0, so it is unaffected by the 1–9–0 ordering choice.)
	Base58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
)
