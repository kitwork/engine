package value

// TrustedHTML is the payload of a String the engine made already-safe HTML (JSON-LD it serialized and
// escaped itself, highlight()'s spans): a template writes it as it is, without escaping, so the page
// needs no raw() for it. Only the engine makes one (Trusted); the JS subset has no way to mark data
// trusted, so it adds no XSS surface. The trust is the payload's type, so it never travels: every
// operation on a String builds a plain string, and a method that gives its receiver back gives it as
// a plain string (ToJson). AsString reads both.
type TrustedHTML string

// Trusted is the String of html, trusted as it is.
func Trusted(html string) Value { return Value{K: String, V: TrustedHTML(html)} }

// IsTrusted reports whether v is a String the engine trusts (Trusted).
func (v Value) IsTrusted() bool {
	_, ok := v.V.(TrustedHTML)
	return ok && v.K == String
}

// AsString is the text v holds as a String payload, plain or trusted; false when v holds none.
func (v Value) AsString() (string, bool) {
	switch s := v.V.(type) {
	case string:
		return s, true
	case TrustedHTML:
		return string(s), true
	}
	return "", false
}
