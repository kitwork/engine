package htmlattr

import "testing"

func TestDecodeUsesHTMLAttributeContext(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "ordinary named", raw: `count &lt; 2 &amp;&amp; ready`, want: `count < 2 && ready`},
		{name: "numeric", raw: `&#65;&#x1F600;`, want: "A😀"},
		{name: "invalid numeric", raw: "&#0; &#xD800; &#x80; &#x;", want: "� � € &#x;"},
		{name: "legacy delimiter", raw: `&copy test`, want: "© test"},
		{name: "ambiguous alphanumeric", raw: `&notit; &copycat`, want: `&notit; &copycat`},
		{name: "ambiguous equals", raw: `&not= &copy=`, want: `&not= &copy=`},
		{name: "duplicate ambiguous ampersands", raw: `&notit;&copycat&&notin;`, want: `&notit;&copycat&∉`},
		{name: "longer named reference", raw: `&notin;`, want: "∉"},
		{name: "longest named reference", raw: `&CounterClockwiseContourIntegral;`, want: "∳"},
		{name: "crlf preprocessing", raw: "a\r\nb\rc\nd", want: "a\nb\nc\nd"},
		{name: "nul preprocessing", raw: "a\x00b", want: "a�b"},
		{name: "invalid utf8 preprocessing", raw: string([]byte{'a', 0xff, 'b'}), want: "a�b"},
		{name: "valid utf8 survives preprocessing", raw: "😀\r\né\x00", want: "😀\né�"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Decode(test.raw); got != test.want {
				t.Fatalf("Decode(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestDecodeUsesWHATWGMalformedUTF8ReplacementBoundaries(t *testing.T) {
	replacement := "�"
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{name: "truncated three byte sequence before ASCII", raw: []byte{'a', 0xe2, 0x82, 'b'}, want: "a" + replacement + "b"},
		{name: "truncated four byte sequence before ASCII", raw: []byte{'a', 0xf0, 0x9f, 0x98, 'b'}, want: "a" + replacement + "b"},
		{name: "truncated sequence at EOF", raw: []byte{0xe2, 0x82}, want: replacement},
		{name: "overlong three byte sequence", raw: []byte{0xe0, 0x80, 0x80}, want: replacement + replacement + replacement},
		{name: "UTF-16 surrogate sequence", raw: []byte{0xed, 0xa0, 0x80}, want: replacement + replacement + replacement},
		{name: "overlong four byte sequence", raw: []byte{0xf0, 0x80, 0x80, 0x80}, want: replacement + replacement + replacement + replacement},
		{name: "above Unicode maximum", raw: []byte{0xf4, 0x90, 0x80, 0x80}, want: replacement + replacement + replacement + replacement},
		{name: "stray continuations", raw: []byte{0x80, 0x80}, want: replacement + replacement},
		{name: "invalid sequence reconsumes CRLF", raw: []byte{0xe2, 0x82, '\r', '\n'}, want: replacement + "\n"},
		{name: "invalid sequence reconsumes valid lead", raw: []byte{0xe2, 0x82, 0xc3, 0xa9}, want: replacement + "é"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Decode(string(test.raw)); got != test.want {
				t.Fatalf("Decode(%x) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}
