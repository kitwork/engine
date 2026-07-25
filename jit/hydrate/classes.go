package hydrate

// Class-name extraction for the CSS JIT.
//
// The CSS JIT emits only the utilities a page actually uses, which it learns by reading the page.
// A STATIC class="…" is easy. A class chosen at runtime — data-kit-class="open ? 'text-red' :
// 'text-green'" — is not: nothing in the HTML says which of the two the page will land on, and the
// JIT must emit BOTH or the page breaks the moment the condition flips.
//
// Scanning the attribute with a regex would be guesswork. It does not have to be: the expression
// already compiles to a structured IR here, so the names can be read off the tree exactly. Every
// syntactic form the grammar allows is covered by the same walk, so authors are not pushed into one
// shape — object, ternary, array and plain string all work, and nest freely:
//
//	{ 'is-open': open }                 → is-open
//	open ? 'text-red' : 'text-green'    → text-red, text-green
//	['card', active ? 'ring' : '']      → card, ring
//
// The ONE thing that cannot work is building a name at runtime — 'text-' + color. The tree proves
// it: the literal is "text-", and no such class exists to emit. That is reported rather than
// guessed at, because the alternative is a page whose styling silently disappears for some values.

// ClassLiterals returns every class name that appears in a class expression as a string literal —
// exactly the set the CSS JIT can emit for it. Order follows the source; duplicates are removed.
// A compile error is returned as-is so the caller can report it the same way it reports any other
// bad expression.
func ClassLiterals(expr string) ([]string, error) {
	node, err := Compile(expr)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	collectClassLiterals(node, seen, &out)
	return out, nil
}

// HasConstructedClass reports whether a class expression builds a name by concatenation, i.e.
// whether it contains a `+` with a string operand. Such a name never appears whole in the source,
// so the JIT cannot emit it — the caller should tell the author instead of shipping a page that
// styles correctly only for the values that happen to have been seen.
//
// Arithmetic is not concatenation: `{ on: n + 1 > 3 }` has no string operand and is left alone.
func HasConstructedClass(expr string) bool {
	node, err := Compile(expr)
	if err != nil {
		return false // a broken expression is reported by the compiler, not by this check
	}
	return hasStringConcat(node)
}

func collectClassLiterals(node any, seen map[string]bool, out *[]string) {
	parts, ok := node.([]any)
	if !ok || len(parts) == 0 {
		return
	}
	op, _ := parts[0].(string)

	switch op {
	case "#":
		// ["#", literal] — a literal. Only strings can name a class; numbers and bools cannot.
		if len(parts) > 1 {
			if s, ok := parts[1].(string); ok {
				add(s, seen, out)
			}
		}
		return

	case "{}":
		// ["{}", [[key, value], …]] — object KEYS are raw strings, not ["#", …] nodes, so they are
		// read directly. { 'is-open': open } means "add is-open when open is truthy".
		if len(parts) > 1 {
			pairs, _ := parts[1].([]any)
			for _, p := range pairs {
				pair, ok := p.([]any)
				if !ok || len(pair) == 0 {
					continue
				}
				if k, ok := pair[0].(string); ok {
					add(k, seen, out)
				}
				if len(pair) > 1 {
					collectClassLiterals(pair[1], seen, out) // a value may itself name classes
				}
			}
		}
		return

	case "$":
		// ["$", name] — a scope read. The name is a VARIABLE, never a class; walking into it would
		// emit CSS for every identifier on the page.
		return
	}

	// Everything else — ternary, array, binary, call, … — is a node whose children may hold
	// literals. ["[]", [items]] nests one level deeper, so plain []any is walked too.
	for _, child := range parts[1:] {
		switch v := child.(type) {
		case []any:
			if isNode(v) {
				collectClassLiterals(v, seen, out)
			} else {
				for _, item := range v {
					collectClassLiterals(item, seen, out)
				}
			}
		}
	}
}

// hasStringConcat looks for a `+` whose operands include a string literal.
func hasStringConcat(node any) bool {
	parts, ok := node.([]any)
	if !ok || len(parts) == 0 {
		return false
	}
	if op, _ := parts[0].(string); op == "+" {
		for _, operand := range parts[1:] {
			if lit, ok := operand.([]any); ok && len(lit) > 1 {
				if o, _ := lit[0].(string); o == "#" {
					if _, isStr := lit[1].(string); isStr {
						return true
					}
				}
			}
		}
	}
	for _, child := range parts[1:] {
		switch v := child.(type) {
		case []any:
			if isNode(v) {
				if hasStringConcat(v) {
					return true
				}
			} else {
				for _, item := range v {
					if hasStringConcat(item) {
						return true
					}
				}
			}
		}
	}
	return false
}

// isNode distinguishes an IR NODE (["op", …]) from a plain LIST of nodes (the payload of "[]" and
// "{}"), which is the only ambiguity in the encoding: both are []any.
func isNode(v []any) bool {
	if len(v) == 0 {
		return false
	}
	_, ok := v[0].(string)
	return ok
}

func add(s string, seen map[string]bool, out *[]string) {
	for _, f := range fields(s) {
		if f != "" && !seen[f] {
			seen[f] = true
			*out = append(*out, f)
		}
	}
}

// fields splits on ASCII whitespace: one literal may carry several classes ('card ring shadow').
func fields(s string) []string {
	var out []string
	start := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}
