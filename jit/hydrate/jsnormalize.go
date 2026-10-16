package hydrate

import (
	"sort"
	"unicode/utf16"
)

// normalize is String.prototype.normalize in form NFC, NFD, NFKC or NFKD, as Unicode's annex 15 says
// and as node's ICU tables have it (jsnormtable.go): decompose each character fully (a Hangul syllable by
// rule), order the marks by their combining class, and for NFC and NFKC compose again what a mark that
// nothing blocks makes with its starter. 16/10/2026: the server had no normalize; a Vietnamese word typed
// with combining marks and one typed precomposed now compare equal after normalize() on both ends.
func normalize(s, form string) any {
	if ascii(s) {
		return s
	}
	compat := form == "NFKC" || form == "NFKD"
	compose := form == "NFC" || form == "NFKC"
	// work on code points; a lone half of a pair stays as itself
	var cps []rune
	u := units(s)
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			c = utf16.DecodeRune(c, rune(u[i+1]))
			i++
		}
		cps = append(cps, c)
	}
	var out []rune
	for _, c := range cps {
		out = decompose(out, c, compat)
	}
	reorder(out)
	if compose {
		out = composeAll(out)
	}
	var back []uint16
	for _, c := range out {
		if c >= 0x10000 {
			a, b := utf16.EncodeRune(c)
			back = append(back, uint16(a), uint16(b))
		} else {
			back = append(back, uint16(c))
		}
	}
	return fromUnits(back)
}

const (
	hangulBase  = 0xAC00
	hangulL     = 0x1100
	hangulV     = 0x1161
	hangulT     = 0x11A7
	hangulLN    = 19
	hangulVN    = 21
	hangulTN    = 28
	hangulNN    = hangulVN * hangulTN
	hangulCount = hangulLN * hangulNN
)

func decompose(out []rune, c rune, compat bool) []rune {
	if c >= hangulBase && c < hangulBase+hangulCount {
		i := c - hangulBase
		out = append(out, hangulL+i/hangulNN, hangulV+(i%hangulNN)/hangulTN)
		if t := i % hangulTN; t != 0 {
			out = append(out, hangulT+t)
		}
		return out
	}
	if compat {
		if d, ok := compatibilityDecomposition[c]; ok {
			return append(out, d...)
		}
	}
	if d, ok := canonicalDecomposition[c]; ok {
		return append(out, d...)
	}
	return append(out, c)
}

// reorder sorts each run of marks by combining class, keeping marks of one class in order.
func reorder(s []rune) {
	for i := 0; i < len(s); {
		if combiningRank[s[i]] == 0 {
			i++
			continue
		}
		j := i
		for j < len(s) && combiningRank[s[j]] != 0 {
			j++
		}
		run := s[i:j]
		sort.SliceStable(run, func(a, b int) bool { return combiningRank[run[a]] < combiningRank[run[b]] })
		i = j
	}
}

// composePair is the character a starter and the next character make, or -1.
func composePair(a, b rune) rune {
	if a >= hangulL && a < hangulL+hangulLN && b >= hangulV && b < hangulV+hangulVN {
		return hangulBase + ((a-hangulL)*hangulVN+(b-hangulV))*hangulTN
	}
	if a >= hangulBase && a < hangulBase+hangulCount && (a-hangulBase)%hangulTN == 0 && b > hangulT && b < hangulT+hangulTN {
		return a + (b - hangulT)
	}
	if c, ok := composePairs[[2]rune{a, b}]; ok {
		return c
	}
	return -1
}

// composeAll is the canonical composition of a decomposed, ordered string.
func composeAll(s []rune) []rune {
	if len(s) == 0 {
		return s
	}
	starter := -1
	last := 0
	if combiningRank[s[0]] == 0 {
		starter = 0
	} else {
		last = 256
	}
	out := s[:1]
	for _, c := range s[1:] {
		rank := int(combiningRank[c])
		if starter >= 0 && (last < rank || last == 0) {
			if composed := composePair(out[starter], c); composed >= 0 {
				out[starter] = composed
				continue
			}
		}
		if rank == 0 {
			starter = len(out)
		}
		last = rank
		out = append(out, c)
	}
	return out
}
