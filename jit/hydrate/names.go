package hydrate

// Which names does an expression READ?
//
// The kernel is deliberately forgiving: a name nothing declares reads as 0, so `data-kit-show="dark"`
// where the boundary holds `darkMode` simply never shows — no error, no clue, and the markup looks
// right. That forgiveness is correct at runtime (a half-built page must not explode), but it makes
// a typo the single most expensive mistake in an authored page.
//
// The server can do better, because it has the expression AND the tree. Reading the names off the
// compiled IR is exact — every syntactic form the grammar allows goes through the same walk, and a
// member name (`user.email`) or an object key is NOT a read, so only real scope lookups count.
// Lambda parameters are bound by the lambda, so they are removed from its body's reads: in
// `items.filter(item => item.done)`, `item` is not something the boundary must declare.

// ReadNames returns every scope name an expression reads, in source order, deduplicated. The `$`
// page-scope handle is reported as "$" so a caller can decide what it means.
func ReadNames(expr string) ([]string, error) {
	node, err := Compile(expr)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	collectReadNames(node, map[string]bool{}, seen, &out)
	return out, nil
}

// ReadNamesAttribute is ReadNames for a value captured from authored HTML, decoded exactly as the
// browser decodes it.
func ReadNamesAttribute(raw string) ([]string, error) {
	return ReadNames(authoredAttribute(raw))
}

// collectReadNames walks the IR. `bound` holds the lambda parameters currently in scope, which
// shadow the boundary and must not be reported.
func collectReadNames(node any, bound map[string]bool, seen map[string]bool, out *[]string) {
	parts, ok := node.([]any)
	if !ok || len(parts) == 0 {
		return
	}
	op, _ := parts[0].(string)

	switch op {
	case "#":
		return // a literal reads nothing

	case "$":
		// ["$", name] — the only shape that reads a scope name.
		if len(parts) > 1 {
			if name, ok := parts[1].(string); ok && !bound[name] && !seen[name] {
				seen[name] = true
				*out = append(*out, name)
			}
		}
		return

	case ".":
		// [".", object, "member"] — the member is a raw string, not a lookup. Only the object reads.
		if len(parts) > 1 {
			collectReadNames(parts[1], bound, seen, out)
		}
		return

	case "=>":
		// ["=>", params, body] — the parameters shadow the boundary inside the body.
		if len(parts) < 3 {
			return
		}
		inner := map[string]bool{}
		for name := range bound {
			inner[name] = true
		}
		if rawParams, ok := parts[1].([]any); ok {
			for _, raw := range rawParams {
				if name, ok := raw.(string); ok {
					inner[name] = true
				}
			}
		}
		collectReadNames(parts[2], inner, seen, out)
		return

	case "{}":
		// ["{}", [[key, value], …]] — keys are raw strings; only the values read.
		if len(parts) > 1 {
			if pairs, ok := parts[1].([]any); ok {
				for _, pair := range pairs {
					if entry, ok := pair.([]any); ok && len(entry) > 1 {
						collectReadNames(entry[1], bound, seen, out)
					}
				}
			}
		}
		return
	}

	// Every other operator is structural: walk its operands.
	for _, part := range parts[1:] {
		switch value := part.(type) {
		case []any:
			// An argument list is a slice of nodes; a node is itself a slice whose head is a string.
			if len(value) > 0 {
				if _, isOp := value[0].(string); isOp {
					collectReadNames(value, bound, seen, out)
					continue
				}
			}
			for _, item := range value {
				collectReadNames(item, bound, seen, out)
			}
		}
	}
}
