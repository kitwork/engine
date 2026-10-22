package richtext

import (
	"html"
	"strings"
)

// NormalizeAddress returns an address with the characters a browser's URL parser reads past taken out
// or read as it reads them: C0 controls (NUL, TAB, CR and LF among them) wherever they are, spaces in
// front, and a backslash, which is a slash. "java\tscript:" is then "javascript:", and "/\evil.example"
// and "/<TAB>/evil.example" are "//evil.example": another host, not a path on the site. Every check of
// a link address reads it through this function, here and in the ui package.
func NormalizeAddress(address string) string {
	clean := true
	for i := 0; i < len(address); i++ {
		if c := address[i]; c < 0x20 || c == '\\' || c == ' ' && i == 0 {
			clean = false
			break
		}
	}
	if clean {
		return address
	}
	var b strings.Builder
	for i := 0; i < len(address); i++ {
		switch c := address[i]; {
		case c < 0x20:
		case c == ' ' && b.Len() == 0:
		case c == '\\':
			b.WriteByte('/')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Link checks the address of a link as written in HTML, an href's value: its character references are
// decoded once (decodeAttribute), it is normalized (NormalizeAddress), and it is kept only when it goes
// to a web page (http:, https:), an e-mail (mailto:), a phone number (tel:) or a path on the site (one
// "/", not followed by another). "//host", "#top", "?q", a relative path, a bare host, javascript:,
// data:, vbscript: and everything else are refused. The address returned is the one that was checked:
// escaped on output, it is exactly what a browser reads.
func Link(address string) (string, bool) {
	address = NormalizeAddress(decodeAttribute(strings.ToValidUTF8(address, "\uFFFD")))
	if allowedLink(address) {
		return address, true
	}
	return "", false
}

// decodeAttribute decodes the character references of an attribute's value once, with
// html.UnescapeString, keeping the one rule a browser has for attributes: a named reference written
// without ";" and followed by "=", a letter or a digit is text there, so "?lang=vi&region=vn" keeps
// "&region" (html.UnescapeString alone would make it "\u00AEion").
func decodeAttribute(value string) string {
	i := strings.IndexByte(value, '&')
	if i < 0 {
		return value
	}
	var b strings.Builder
	last := 0
	for ; i >= 0; i = nextAmpersand(value, i+1) {
		k := i + 1
		for k < len(value) && isAlphanumeric(value[k]) {
			k++
		}
		name := value[i+1 : k]
		if name == "" || k < len(value) && value[k] == ';' && wholeReference(name) {
			continue // a number, or a reference written whole: decoded
		}
		probe := "&" + name
		if len(probe) > 40 {
			probe = probe[:40] // longer than any reference: what is left is a tail all the same
		}
		decoded := html.UnescapeString(probe)
		if decoded == probe {
			continue // no reference at all
		}
		// html.UnescapeString decoded a legacy reference at the front of name; the letters and digits
		// it left are what follows the reference
		tail := len(decoded)
		for tail > 0 && isAlphanumeric(decoded[tail-1]) {
			tail--
		}
		if tail < len(decoded) || k < len(value) && value[k] == '=' {
			b.WriteString(value[last:i])
			b.WriteString("&amp;") // the "&" stays text through the decoding below
			last = i + 1
		}
	}
	if last == 0 {
		return html.UnescapeString(value)
	}
	b.WriteString(value[last:])
	return html.UnescapeString(b.String())
}

func nextAmpersand(s string, from int) int {
	if j := strings.IndexByte(s[from:], '&'); j >= 0 {
		return from + j
	}
	return -1
}

// wholeReference reports whether "&name;" is a reference of its own (not a shorter legacy one with
// letters after it).
func wholeReference(name string) bool {
	if len(name) > 40 {
		return false
	}
	written := "&" + name + ";"
	decoded := html.UnescapeString(written)
	return decoded != written && !strings.HasSuffix(decoded, name[len(name)-1:]+";")
}

func isAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// allowedLink reports whether a normalized address is one a link may hold.
func allowedLink(address string) bool {
	if strings.HasPrefix(address, "/") {
		return !strings.HasPrefix(address, "//")
	}
	for _, scheme := range [...]string{"http:", "https:", "mailto:", "tel:"} {
		if hasPrefixFold(address, scheme) {
			return true
		}
	}
	return false
}

// External reports whether a link leaves a page served from origin ("https://kitwork.io", or a bare
// host): a web address (http:, https:, or "//" in front) to another host, or any web address when
// origin is "" or the address names no host. A path, an anchor, an e-mail and a phone number never
// leave. The host is the one a browser reads: the address normalized (NormalizeAddress), then past
// the scheme any number of slashes ("http:host" and "http:///host" are host), up to "/", "?" or "#",
// after the last "@", without the port, in any case. A "//" further on is part of the path:
// "http:evil.example/x//kitwork.io" goes to evil.example. Display marks a link with it, and so does
// the ui package's <link>: one rule for both.
func External(address, origin string) bool {
	address = NormalizeAddress(address)
	var rest string
	switch {
	case hasPrefixFold(address, "http:"), hasPrefixFold(address, "https:"):
		rest = address[strings.IndexByte(address, ':')+1:]
	case strings.HasPrefix(address, "//"):
		rest = address
	default:
		return false
	}
	if origin == "" {
		return true
	}
	host := hostName(strings.TrimLeft(rest, "/"))
	if i := strings.Index(origin, "://"); i >= 0 {
		origin = origin[i+3:]
	}
	return host == "" || host != hostName(origin)
}

// hostName is the host at the start of rest (what follows a web address's scheme and slashes), lower
// case, without the user in front or the port after it.
func hostName(rest string) string {
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		rest = rest[i+1:]
	}
	if strings.HasPrefix(rest, "[") { // an IPv6 address: [::1]:8080
		if i := strings.IndexByte(rest, ']'); i >= 0 {
			rest = rest[:i+1]
		}
	} else if i := strings.LastIndexByte(rest, ':'); i >= 0 {
		rest = rest[:i]
	}
	return strings.ToLower(rest)
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
