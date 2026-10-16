package hydrate

import (
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Literal writes v as an expression of the grammar that reads back as v, for the kernel to read a
// value the server read: a component's input the server computed, which the browser seeds the
// instance from (data-kitwork-scope). NaN and the infinities are 0 / 0, 1 / 0 and -1 / 0, undefined
// is [][0], a list with holes is made with Array(n) and concat, a lambda is its source, a global, a
// built-in function, a bound one and a raw JSON value the expression that gives them; a host's map has
// its keys sorted, an object the expression made its own order (16/10/2026: NaN, the infinities,
// undefined, holes, lambdas and globals were all null, and anything 17 deep, so the browser's instance
// read null where the server had painted with the value). exact is false where a part has no
// expression that makes it — an iterator, a promise, a host's Go function, an object with no prototype,
// a "__proto__" key, a value that holds itself — which is written as undefined (null inside itself):
// the server then paints with what the literal reads back as, so both ends start from one value.
// Past the kernel's budget (a literal of more than evalBudget values, which the kernel cannot read)
// the writing stops, the brackets closed: the kernel refuses it all the same.
func Literal(v any) (text string, exact bool) {
	w := &literalWriter{exact: true}
	w.value(v, nil)
	return w.b.String(), w.exact
}

type literalWriter struct {
	b     strings.Builder
	exact bool
	count int // values written: the kernel walks at least one node for each
}

// literalID is what an object or a list is: its map, its slice's first item and length, its pointer.
type literalID struct {
	pointer uintptr
	length  int
}

func (w *literalWriter) value(v any, path []literalID) {
	w.count++
	var id literalID
	switch t := v.(type) {
	case []any:
		if cap(t) > 0 && len(t) > 0 {
			id = literalID{pointer: reflect.ValueOf(t).Pointer(), length: len(t)}
		}
	case map[string]any:
		if t != nil {
			id = literalID{pointer: reflect.ValueOf(t).Pointer(), length: -1}
		}
	case *Object:
		id = literalID{pointer: reflect.ValueOf(t).Pointer(), length: -2}
	}
	if id != (literalID{}) {
		for _, on := range path {
			if on == id {
				// a value that holds itself ({{ x = $ }} puts the page's scope into itself): null where it
				// comes back, as it was written before (14/10/2026: written again to a depth bound, it
				// doubled at every level, some 15 GB for one request)
				w.exact = false
				w.b.WriteString("null")
				return
			}
		}
		path = append(path, id)
	}
	switch t := norm(v).(type) {
	case nil:
		w.b.WriteString("null")
	case undefinedType:
		w.b.WriteString("[][0]")
	case bool:
		w.b.WriteString(strconv.FormatBool(t))
	case float64:
		switch {
		case t != t:
			w.b.WriteString("0 / 0")
		case math.IsInf(t, 1):
			w.b.WriteString("1 / 0")
		case math.IsInf(t, -1):
			w.b.WriteString("-1 / 0")
		default:
			w.b.WriteString(strconv.FormatFloat(t, 'f', -1, 64))
		}
	case string:
		w.text(t)
	case []any:
		w.list(t, path)
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w.object(keys, func(k string) any { return t[k] }, path)
	case *Object:
		if t.bare {
			w.exact = false // an object with no prototype (a match's groups) reads back with one
		}
		w.object(t.Keys(), func(k string) any { return t.values[k] }, path)
	case *Lambda:
		if t.Source == "" {
			w.lost()
			return
		}
		w.b.WriteString("(" + t.Source + ")")
	case globalRef:
		w.b.WriteString(string(t))
	case nativeFunc:
		w.b.WriteString(nativeHolder(t.owner) + "." + t.name)
	case *boundFunc:
		w.b.WriteString("(")
		w.value(t.target, path)
		w.b.WriteString(").bind(")
		w.value(t.this, path)
		for _, a := range t.args {
			w.b.WriteString(", ")
			w.value(a, path)
		}
		w.b.WriteString(")")
	case *rawJSON:
		w.b.WriteString("JSON.rawJSON(")
		w.text(t.text)
		w.b.WriteString(")")
	default:
		w.lost() // an iterator, a promise, a host's Go function
	}
}

// lost writes undefined for a value no expression of the grammar makes.
func (w *literalWriter) lost() {
	w.exact = false
	w.b.WriteString("[][0]")
}

func (w *literalWriter) text(s string) {
	w.b.WriteString("'" + QuoteText(s) + "'")
}

// list writes a list; one with holes as the runs of its items and Array(n) for each run of holes,
// joined by concat, which keeps a hole a hole.
func (w *literalWriter) list(t []any, path []literalID) {
	holes := 0
	for _, x := range t {
		if isHole(x) {
			holes++
		}
	}
	if holes > 0 && holes == len(t) {
		w.b.WriteString("Array(" + strconv.Itoa(holes) + ")")
		return
	}
	if holes == 0 {
		w.b.WriteString("[")
		for i, x := range t {
			if w.count > evalBudget {
				break // the kernel stops past its budget whatever follows
			}
			if i > 0 {
				w.b.WriteString(", ")
			}
			w.value(x, path)
		}
		w.b.WriteString("]")
		return
	}
	w.b.WriteString("[].concat(")
	for i := 0; i < len(t) && w.count <= evalBudget; {
		if i > 0 {
			w.b.WriteString(", ")
		}
		start := i
		if isHole(t[i]) {
			for i < len(t) && isHole(t[i]) {
				i++
			}
			w.count++
			w.b.WriteString("Array(" + strconv.Itoa(i-start) + ")")
			continue
		}
		w.b.WriteString("[")
		for ; i < len(t) && !isHole(t[i]) && w.count <= evalBudget; i++ {
			if i > start {
				w.b.WriteString(", ")
			}
			w.value(t[i], path)
		}
		w.b.WriteString("]")
	}
	w.b.WriteString(")")
}

func (w *literalWriter) object(keys []string, get func(string) any, path []literalID) {
	w.b.WriteString("{")
	for i, k := range keys {
		if w.count > evalBudget {
			break
		}
		if k == "__proto__" {
			w.exact = false // the kernel and the server drop it as they make the object
		}
		if i > 0 {
			w.b.WriteString(", ")
		}
		w.text(k)
		w.b.WriteString(": ")
		w.value(get(k), path)
	}
	w.b.WriteString("}")
}

// nativeHolder is an expression that reaches a built-in function's owner: the global itself, or a
// value whose prototype holds the method.
func nativeHolder(owner string) string {
	switch owner {
	case "String.prototype":
		return "''"
	case "Array.prototype":
		return "[]"
	case "Number.prototype":
		return "(0)"
	case "Boolean.prototype":
		return "true"
	case "Object.prototype":
		return "{}"
	case "Function.prototype":
		return "(() => 0)"
	case "ArrayIterator.prototype", "Iterator.prototype":
		return "[].values()"
	}
	return owner
}
