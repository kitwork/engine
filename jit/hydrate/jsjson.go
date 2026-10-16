package hydrate

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kitwork/engine/value"
)

// JSON as JavaScript's: parse keeps an object's keys in the order the text gives them and reads a
// number past the largest as Infinity; stringify writes keys in the object's order, escapes only what
// JSON must (no < for <, as Go's encoder wrote), -0 as 0, NaN and the infinities as null, leaves
// out undefined and functions, runs a toJSON or a replacer, and indents (16/10/2026: the server had
// Go's encoding/json, which sorted keys and wrote -0 and "<").

func (m *machine) jsonMethod(f *frame, name string, args []any) (any, error) {
	switch name {
	case "parse":
		text, err := m.toString(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		p := &jsonParser{s: text}
		v, err := p.document()
		if err != nil {
			return nil, err
		}
		if reviver := arg(args, 1); callable(reviver) {
			holder := newObject(1)
			holder.set("", v)
			return m.revive(f, holder, "", reviver, 0)
		}
		return v, nil
	case "stringify":
		return m.stringify(f, arg(args, 0), arg(args, 1), arg(args, 2))
	case "rawJSON":
		text, err := m.toString(f, arg(args, 0))
		if err != nil {
			return nil, err
		}
		if text == "" || strings.ContainsAny(text[:1], "\t\n\r ") || strings.ContainsAny(text[len(text)-1:], "\t\n\r ") {
			return nil, syntaxError("Invalid value for JSON.rawJSON")
		}
		p := &jsonParser{s: text}
		v, err := p.document()
		if err != nil {
			return nil, err
		}
		switch v.(type) {
		case []any, *Object:
			return nil, syntaxError("Invalid value for JSON.rawJSON")
		}
		return &rawJSON{text: text}, nil
	case "isRawJSON":
		_, ok := arg(args, 0).(*rawJSON)
		return ok, nil
	}
	return nil, typeError("JSON." + name + " is not a function")
}

// jsonParser reads JSON text as JSON.parse does.
type jsonParser struct {
	s   string
	at  int
	dep int
}

func (p *jsonParser) fail() error {
	if p.at >= len(p.s) {
		return syntaxError("Unexpected end of JSON input")
	}
	return syntaxError("Unexpected token in JSON at position " + strconv.Itoa(p.at))
}

func (p *jsonParser) space() {
	for p.at < len(p.s) {
		switch p.s[p.at] {
		case ' ', '\t', '\n', '\r':
			p.at++
			continue
		}
		return
	}
}

func (p *jsonParser) document() (any, error) {
	p.space()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.at != len(p.s) {
		return nil, p.fail()
	}
	return v, nil
}

func (p *jsonParser) value() (any, error) {
	if p.at >= len(p.s) {
		return nil, p.fail()
	}
	p.dep++
	defer func() { p.dep-- }()
	if p.dep > 5000 {
		return nil, rangeError("Maximum call stack size exceeded")
	}
	switch c := p.s[p.at]; {
	case c == '{':
		p.at++
		obj := newObject(4)
		p.space()
		if p.at < len(p.s) && p.s[p.at] == '}' {
			p.at++
			return obj, nil
		}
		for {
			p.space()
			if p.at >= len(p.s) || p.s[p.at] != '"' {
				return nil, p.fail()
			}
			k, err := p.text()
			if err != nil {
				return nil, err
			}
			p.space()
			if p.at >= len(p.s) || p.s[p.at] != ':' {
				return nil, p.fail()
			}
			p.at++
			p.space()
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			obj.set(k, v)
			p.space()
			if p.at < len(p.s) && p.s[p.at] == ',' {
				p.at++
				continue
			}
			if p.at < len(p.s) && p.s[p.at] == '}' {
				p.at++
				return obj, nil
			}
			return nil, p.fail()
		}
	case c == '[':
		p.at++
		list := newList(0)
		p.space()
		if p.at < len(p.s) && p.s[p.at] == ']' {
			p.at++
			return list, nil
		}
		for {
			p.space()
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
			p.space()
			if p.at < len(p.s) && p.s[p.at] == ',' {
				p.at++
				continue
			}
			if p.at < len(p.s) && p.s[p.at] == ']' {
				p.at++
				return list, nil
			}
			return nil, p.fail()
		}
	case c == '"':
		return p.text()
	case c == 't' && strings.HasPrefix(p.s[p.at:], "true"):
		p.at += 4
		return true, nil
	case c == 'f' && strings.HasPrefix(p.s[p.at:], "false"):
		p.at += 5
		return false, nil
	case c == 'n' && strings.HasPrefix(p.s[p.at:], "null"):
		p.at += 4
		return nil, nil
	case c == '-' || c >= '0' && c <= '9':
		start := p.at
		if c == '-' {
			p.at++
		}
		if p.at >= len(p.s) {
			return nil, p.fail()
		}
		switch {
		case p.s[p.at] == '0':
			p.at++
		case p.s[p.at] >= '1' && p.s[p.at] <= '9':
			for p.at < len(p.s) && p.s[p.at] >= '0' && p.s[p.at] <= '9' {
				p.at++
			}
		default:
			return nil, p.fail()
		}
		if p.at < len(p.s) && p.s[p.at] == '.' {
			p.at++
			digits := p.at
			for p.at < len(p.s) && p.s[p.at] >= '0' && p.s[p.at] <= '9' {
				p.at++
			}
			if p.at == digits {
				return nil, p.fail()
			}
		}
		if p.at < len(p.s) && (p.s[p.at] == 'e' || p.s[p.at] == 'E') {
			p.at++
			if p.at < len(p.s) && (p.s[p.at] == '+' || p.s[p.at] == '-') {
				p.at++
			}
			digits := p.at
			for p.at < len(p.s) && p.s[p.at] >= '0' && p.s[p.at] <= '9' {
				p.at++
			}
			if p.at == digits {
				return nil, p.fail()
			}
		}
		return value.StringToNumber(p.s[start:p.at]), nil
	}
	return nil, p.fail()
}

// text reads a JSON string, the quote at p.at; \uXXXX may name a lone half of a pair, kept as one.
func (p *jsonParser) text() (string, error) {
	p.at++
	var u []uint16
	plain := p.at
	for {
		if p.at >= len(p.s) {
			return "", p.fail()
		}
		c := p.s[p.at]
		switch {
		case c == '"':
			if u == nil {
				out := p.s[plain:p.at]
				p.at++
				return out, nil
			}
			u = append(u, units(p.s[plain:p.at])...)
			p.at++
			return fromUnits(u), nil
		case c < 0x20:
			return "", p.fail()
		case c == '\\':
			u = append(u, units(p.s[plain:p.at])...)
			if u == nil {
				u = []uint16{}
			}
			p.at++
			if p.at >= len(p.s) {
				return "", p.fail()
			}
			switch e := p.s[p.at]; e {
			case '"', '\\', '/':
				u = append(u, uint16(e))
			case 'b':
				u = append(u, '\b')
			case 'f':
				u = append(u, '\f')
			case 'n':
				u = append(u, '\n')
			case 'r':
				u = append(u, '\r')
			case 't':
				u = append(u, '\t')
			case 'u':
				if p.at+4 >= len(p.s) {
					p.at = len(p.s)
					return "", p.fail()
				}
				code, err := strconv.ParseUint(p.s[p.at+1:p.at+5], 16, 16)
				if err != nil {
					return "", p.fail()
				}
				u = append(u, uint16(code))
				p.at += 4
			default:
				return "", p.fail()
			}
			p.at++
			plain = p.at
		default:
			p.at++
		}
	}
}

// revive is InternalizeJSONProperty: the reviver called on every value, the deepest first.
func (m *machine) revive(f *frame, holder any, key string, reviver any, depth int) (any, error) {
	if depth > 5000 {
		return nil, rangeError("Maximum call stack size exceeded")
	}
	val, err := m.get(f, holder, key)
	if err != nil {
		return nil, err
	}
	switch t := val.(type) {
	case []any:
		for i := range t {
			k := primitiveText(float64(i))
			v, err := m.revive(f, t, k, reviver, depth+1)
			if err != nil {
				return nil, err
			}
			if isUndefined(v) {
				t[i] = hole
			} else {
				t[i] = v
			}
		}
	case *Object:
		for _, k := range t.Keys() {
			v, err := m.revive(f, t, k, reviver, depth+1)
			if err != nil {
				return nil, err
			}
			if isUndefined(v) {
				t.remove(k)
			} else {
				t.set(k, v)
			}
		}
	}
	return m.callValue(f, reviver, holder, []any{key, val})
}

// remove deletes the object's key k.
func (o *Object) remove(k string) {
	if _, ok := o.values[k]; !ok {
		return
	}
	delete(o.values, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// stringify is JSON.stringify(v, replacer, space).
func (m *machine) stringify(f *frame, v, replacer, space any) (any, error) {
	s := &jsonWriter{m: m, f: f}
	if callable(replacer) {
		s.replacer = replacer
	} else if list, ok := replacer.([]any); ok {
		seen := map[string]bool{}
		s.allow = []string{}
		for _, it := range list {
			var key string
			switch t := item(it).(type) {
			case string:
				key = t
			case float64:
				key = primitiveText(t)
			default:
				continue
			}
			if !seen[key] {
				seen[key] = true
				s.allow = append(s.allow, key)
			}
		}
	}
	switch t := space.(type) {
	case float64:
		n := math.Min(10, value.ToIntegerOrInfinity(t))
		if n >= 1 {
			s.gap = strings.Repeat(" ", int(n))
		}
	case string:
		u := units(t)
		if len(u) > 10 {
			u = u[:10]
		}
		s.gap = fromUnits(u)
	}
	holder := newObject(1)
	holder.set("", v)
	out, ok, err := s.property(holder, "", v, "")
	if err != nil {
		return nil, err
	}
	if !ok {
		return Undefined, nil
	}
	return out, nil
}

type jsonWriter struct {
	m        *machine
	f        *frame
	replacer any
	allow    []string
	gap      string
	stack    []any
	size     int
}

// property is SerializeJSONProperty: the text of holder[key], whose value is v, or false when JSON
// leaves it out (undefined, a function).
func (s *jsonWriter) property(holder any, key string, v any, indent string) (string, bool, error) {
	if isObject(v) {
		toJSON, err := s.m.get(s.f, v, "toJSON")
		if err != nil {
			return "", false, err
		}
		if callable(toJSON) {
			v, err = s.m.callValue(s.f, toJSON, v, []any{key})
			if err != nil {
				return "", false, err
			}
		}
	}
	if s.replacer != nil {
		var err error
		v, err = s.m.callValue(s.f, s.replacer, holder, []any{key, v})
		if err != nil {
			return "", false, err
		}
	}
	switch t := v.(type) {
	case nil:
		return "null", true, nil
	case bool:
		return primitiveText(t), true, nil
	case string:
		return quoteJSON(t), true, nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return "null", true, nil
		}
		return value.NumberText(t), true, nil
	case *rawJSON:
		return t.text, true, nil
	case []any:
		out, err := s.list(t, indent)
		return out, err == nil, err
	}
	if !isObject(v) || callable(v) {
		return "", false, nil
	}
	out, err := s.object(v, indent)
	return out, err == nil, err
}

func (s *jsonWriter) enter(v any) error {
	for _, on := range s.stack {
		if sameRef(on, v) {
			return typeError("Converting circular structure to JSON")
		}
	}
	if len(s.stack) > 5000 {
		return rangeError("Maximum call stack size exceeded")
	}
	s.stack = append(s.stack, v)
	return nil
}

func (s *jsonWriter) leave() { s.stack = s.stack[:len(s.stack)-1] }

func (s *jsonWriter) grow(n int) error {
	s.size += n
	if s.size > serverStringLimit {
		return errTooLong
	}
	return nil
}

func (s *jsonWriter) list(list []any, indent string) (string, error) {
	if err := s.enter(list); err != nil {
		return "", err
	}
	defer s.leave()
	inner := indent + s.gap
	parts := make([]string, len(list))
	for i, it := range list {
		text, ok, err := s.property(list, primitiveText(float64(i)), item(it), inner)
		if err != nil {
			return "", err
		}
		if !ok {
			text = "null"
		}
		parts[i] = text
		if err := s.grow(len(text)); err != nil {
			return "", err
		}
	}
	if len(parts) == 0 {
		return "[]", nil
	}
	if s.gap == "" {
		return "[" + join16(parts, ",") + "]", nil
	}
	return "[\n" + inner + join16(parts, ",\n"+inner) + "\n" + indent + "]", nil
}

func (s *jsonWriter) object(o any, indent string) (string, error) {
	if err := s.enter(o); err != nil {
		return "", err
	}
	defer s.leave()
	keys := s.allow
	if keys == nil {
		keys = s.enumerableKeys(o)
	}
	inner := indent + s.gap
	var parts []string
	for _, k := range keys {
		v, err := s.m.get(s.f, o, k)
		if err != nil {
			return "", err
		}
		text, ok, err := s.property(o, k, v, inner)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		member := quoteJSON(k) + ":"
		if s.gap != "" {
			member += " "
		}
		parts = append(parts, concat16(member, text))
		if err := s.grow(len(member) + len(text)); err != nil {
			return "", err
		}
	}
	if len(parts) == 0 {
		return "{}", nil
	}
	if s.gap == "" {
		return "{" + join16(parts, ",") + "}", nil
	}
	return "{\n" + inner + join16(parts, ",\n"+inner) + "\n" + indent + "}", nil
}

// enumerableKeys are the keys JSON writes of an object: its own enumerable ones, in order.
func (s *jsonWriter) enumerableKeys(o any) []string {
	switch t := o.(type) {
	case map[string]any, *Object:
		return ownKeys(t)
	case globalRef:
		if t == "Object" {
			return []string{"keys"}
		}
	}
	return nil
}

// quoteJSON is QuoteJSONString: the string in double quotes, \" \\ \b \f \n \r \t escaped, any other
// control character and a lone half of a pair as \u with four lowercase hex digits.
func quoteJSON(text string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(text); {
		if half, lone := loneHalf(text[i:]); lone {
			b.WriteString(`\u` + strconv.FormatUint(uint64(half), 16))
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				hex := strconv.FormatUint(uint64(r), 16)
				b.WriteString(`\u` + strings.Repeat("0", 4-len(hex)) + hex)
			} else {
				b.WriteString(text[i : i+size])
			}
		}
		i += size
	}
	b.WriteByte('"')
	return b.String()
}
