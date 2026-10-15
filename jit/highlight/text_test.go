package highlight

import "testing"

// text is the source as written: one plain token, whatever the source holds.
func TestText(t *testing.T) {
	checkExact(t, "text", []tokenCase{
		{"", nil},
		{"plain words", tokensOf(Plain, "plain words")},
		{`if (x) { return "y" } // z`, tokensOf(Plain, `if (x) { return "y" } // z`)},
		{"a\r\n\tb\x00\xff", tokensOf(Plain, "a\r\n\tb\x00\xff")},
		{"   \n", tokensOf(Plain, "   \n")},
	})
}
