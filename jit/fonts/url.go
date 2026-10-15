package fonts

// A Google Fonts address pasted where router.fonts belongs: the compiler reads the families out of it
// and prints the router.fonts line to write instead.

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Pasted is one family a Google Fonts address asks for.
type Pasted struct {
	// Name is the family as Google spells it when the catalog has it (Known), else as the address
	// wrote it, "+" read as a space.
	Name  string
	Known bool
	// Axes are the axis tags the address names, as written ("ital", "opsz", "wght").
	Axes []string
	// Weights are the single weights the address lists (wght@400;700, or the old :400,700i), ascending;
	// nil when it asks for a range (wght@100..900) or for none.
	Weights []int
	// Italic: the address asks for an italic (ital@1, :400i, :italic).
	Italic bool
}

// ParseURL reads the families out of a pasted Google Fonts address — the address alone, or the
// <link href="…"> or @import url(…) it came in — in every form Google has handed out:
//
//	https://fonts.googleapis.com/css2?family=Google+Sans+Flex:opsz,wght@6..144,1..1000&family=Itim&display=swap
//	https://fonts.googleapis.com/css?family=Roboto:300,400,700i|Open+Sans&subset=vietnamese
//	https://fonts.google.com/specimen/Itim
//	https://fonts.google.com/share?selection.family=Itim|Inter:wght@400;700
//
// &amp; in HTML is read as &, and so are &#38;, &#038; (WordPress) and &#x26;. The families come back
// in the order written, each once.
func ParseURL(text string) ([]Pasted, error) {
	address, err := pastedAddress(text)
	if err != nil {
		return nil, err
	}
	var items []string
	switch host, path := address.Host, strings.TrimSuffix(address.Path, "/"); {
	case host == cssHost && path == "/css2":
		items = queryValues(address.RawQuery, "family", "")
	case host == cssHost && path == "/css":
		items = queryValues(address.RawQuery, "family", "|")
	case host == "fonts.google.com" && strings.HasPrefix(path, "/specimen/"):
		name := strings.TrimPrefix(path, "/specimen/")
		name, _, _ = strings.Cut(name, "/")
		items = []string{strings.ReplaceAll(name, "+", " ")}
	case host == "fonts.google.com" && path == "/share":
		items = queryValues(address.RawQuery, "selection.family", "|")
	default:
		return nil, errors.New("địa chỉ " + strconv.Quote(address.String()) + " không phải địa chỉ CSS của Google Fonts " +
			"(fonts.googleapis.com/css2?family=…) — hãy dán địa chỉ trong thẻ <link> Google Fonts đưa cho")
	}
	var out []Pasted
	seen := map[string]int{}
	for _, item := range items {
		p, ok := pastedFamily(item)
		if !ok {
			continue
		}
		key := familyKey(p.Name)
		if i, twice := seen[key]; twice {
			merged := &out[i]
			merged.Italic = merged.Italic || p.Italic
			merged.Weights = mergeWeights(merged.Weights, p.Weights)
			continue
		}
		seen[key] = len(out)
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errors.New("địa chỉ Google Fonts này không có họ chữ nào (family=…) — hãy chọn họ chữ trên fonts.google.com rồi dán lại thẻ <link>")
	}
	return out, nil
}

// ampersandRe is & as HTML writes it in an attribute: &amp;, &#38;, &#038;, &#x26;.
var ampersandRe = regexp.MustCompile(`(?i)&(?:amp|#0*38|#x0*26);`)

// pastedAddress finds the Google Fonts address in what was pasted.
func pastedAddress(text string) (*url.URL, error) {
	text = ampersandRe.ReplaceAllString(text, "&")
	lower := asciiLower(text) // every index found in lower is the same one in text
	at := -1
	for _, host := range []string{cssHost + "/", "fonts.google.com/"} {
		if i := strings.Index(lower, host); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at < 0 {
		return nil, errors.New("không thấy địa chỉ Google Fonts trong đoạn đã dán — hãy dán địa chỉ " +
			"https://fonts.googleapis.com/css2?family=… hoặc cả thẻ <link href=\"…\"> Google Fonts đưa cho")
	}
	start := at
	for _, scheme := range []string{"https://", "http://", "//"} {
		if start >= len(scheme) && lower[start-len(scheme):start] == scheme {
			start -= len(scheme)
			break
		}
	}
	end := len(text)
	if i := strings.IndexAny(text[at:], "\"'`<> \t\r\n)"); i >= 0 {
		end = at + i
	}
	raw := text[start:end]
	if i := strings.Index(raw, "//"); i >= 0 {
		raw = raw[i+2:]
	}
	address, err := url.Parse("https://" + raw)
	if err != nil {
		return nil, errors.New("địa chỉ Google Fonts đã dán bị hỏng (" + err.Error() + ") — hãy chép lại cả địa chỉ")
	}
	address.Host = strings.ToLower(address.Host)
	return address, nil
}

// asciiLower folds A–Z only, byte for byte, so the result is exactly as long as s. strings.ToLower is
// not: 'Ⱥ' is two bytes and 'ⱥ' three, and an invalid byte becomes U+FFFD (three), so an index found in
// its result would point elsewhere in s — past its end, after enough of them.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// queryValues are the values of one query parameter, each split on sep when sep is given. The query
// is read by hand: url.ParseQuery refuses the ";" every css2 address has (wght@400;700).
func queryValues(rawQuery, key, sep string) []string {
	var out []string
	for _, pair := range strings.Split(rawQuery, "&") {
		k, v, _ := strings.Cut(pair, "=")
		if unescaped, err := url.QueryUnescape(k); err == nil {
			k = unescaped
		}
		if k != key {
			continue
		}
		if unescaped, err := url.QueryUnescape(v); err == nil {
			v = unescaped
		} else {
			v = strings.ReplaceAll(v, "+", " ")
		}
		if sep == "" {
			out = append(out, v)
			continue
		}
		out = append(out, strings.Split(v, sep)...)
	}
	return out
}

// pastedFamily reads one family=… value: "Name", "Name:axes@tuples" (css2) or "Name:variants[:subsets]"
// (css).
func pastedFamily(item string) (Pasted, bool) {
	name, spec, _ := strings.Cut(item, ":")
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return Pasted{}, false
	}
	p := Pasted{Name: name}
	if f, ok := Find(name); ok {
		p.Name, p.Known = f.Name, true
	}
	if tags, tuples, css2 := strings.Cut(spec, "@"); css2 {
		p.Axes = strings.Split(tags, ",")
		for _, tuple := range strings.Split(tuples, ";") {
			values := strings.Split(tuple, ",")
			for i, tag := range p.Axes {
				if i >= len(values) {
					break
				}
				switch value := strings.TrimSpace(values[i]); tag {
				case "ital":
					p.Italic = p.Italic || value == "1"
				case "wght":
					if n, err := strconv.Atoi(value); err == nil && n >= 1 && n <= 1000 {
						p.Weights = mergeWeights(p.Weights, []int{n})
					}
				}
			}
		}
		return p, true
	}
	variants, _, _ := strings.Cut(spec, ":") // css: Name:400,700i:latin,vietnamese
	for _, variant := range strings.Split(variants, ",") {
		variant = strings.ToLower(strings.TrimSpace(variant))
		if variant == "" {
			continue
		}
		digits := strings.TrimRightFunc(variant, func(r rune) bool { return !unicode.IsDigit(r) })
		style := variant[len(digits):]
		weight := 400
		if digits != "" {
			n, err := strconv.Atoi(digits)
			if err != nil || n < 1 || n > 1000 {
				continue
			}
			weight = n
		}
		switch style {
		case "", "regular", "r":
		case "i", "italic":
			p.Italic = true
		case "bold", "b":
			weight = 700
		case "bolditalic", "bi":
			weight, p.Italic = 700, true
		default:
			continue
		}
		p.Weights = mergeWeights(p.Weights, []int{weight})
	}
	return p, true
}

// mergeWeights is a ∪ b, ascending.
func mergeWeights(a, b []int) []int {
	out := append([]int(nil), a...)
	for _, w := range b {
		i := 0
		for i < len(out) && out[i] < w {
			i++
		}
		if i < len(out) && out[i] == w {
			continue
		}
		out = append(out[:i], append([]int{w}, out[i:]...)...)
	}
	return out
}

// RouterFontsLine is the router.fonts line for families — what the compiler prints in place of a
// pasted Google Fonts address. Each family gets a role by its category: the first monospace family
// is code, the first handwriting family is handwriting, the first of any other is body; a second of
// a kind is keyed by its own name ("Playwrite CA Guides" → playwriteCaGuides), for the site to rename.
// A family named twice is written once, and no key is written twice.
//
//	router.fonts({ body: "Google Sans Flex", code: "JetBrains Mono", handwriting: "Itim" })
func RouterFontsLine(names []string) string {
	taken := map[string]bool{}
	written := map[string]bool{}
	var parts []string
	for _, name := range names {
		spelled := strings.Join(strings.Fields(name), " ")
		if spelled == "" || written[familyKey(spelled)] {
			continue
		}
		written[familyKey(spelled)] = true
		role := "body"
		if f, ok := Find(spelled); ok {
			spelled = f.Name
			switch f.Category {
			case "Monospace":
				role = "code"
			case "Handwriting":
				role = "handwriting"
			}
		}
		if taken[role] {
			role = roleOf(spelled)
		}
		for n, base := 2, role; taken[role]; n++ {
			role = base + strconv.Itoa(n)
		}
		taken[role] = true
		parts = append(parts, objectKey(role)+": "+strconv.Quote(spelled))
	}
	return "router.fonts({ " + strings.Join(parts, ", ") + " })"
}

// roleOf is a family name as an object key: its words in camelCase ("Playwrite CA Guides" →
// playwriteCaGuides).
func roleOf(name string) string {
	var b strings.Builder
	for i, word := range strings.Fields(name) {
		word = strings.ToLower(word)
		if i > 0 && word != "" {
			word = strings.ToUpper(word[:1]) + word[1:]
		}
		b.WriteString(word)
	}
	return b.String()
}

// objectKey is a key as a JavaScript object literal writes it: bare, quoted when it does not start
// with a letter ("42dotSans").
func objectKey(key string) string {
	if key == "" || !unicode.IsLetter(rune(key[0])) {
		return strconv.Quote(key)
	}
	return key
}
