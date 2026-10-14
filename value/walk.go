package value

import (
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A walk over a value goes into every object and array it holds: JSON.stringify, its text, its Go
// form, deep equality, flat(). Two shapes would take a walk down forever: a value that holds itself
// (o.self = o, a.push(a), a .ui page's {{ x = $ }}), and one nested deeper than a stack can hold (a
// loop that wraps a value a million times). In Go either ends in a stack overflow, which no recover()
// catches: the process dies, and every site of the host with it, for one tenant's value. So a walk
// keeps the path it is on: an object or array met again on its own path is a cycle, a path longer
// than MaxDepth is too deep, and the walk ends there in an error (or, where it cannot fail, a mark in
// the text) — never in Go's stack. The same object reached twice by two paths is not a cycle: only a
// value that holds itself is (14/10/2026).

// MaxDepth is how many objects and arrays a value may nest one inside the other: 10000, as deep as
// JSON.parse reads (encoding/json's own bound), so every value JSON.parse gives can be written back.
const MaxDepth = 10000

// WalkError is why a walk over a value stopped before its end: the value holds itself (Cycle), or it
// nests deeper than MaxDepth.
type WalkError struct {
	Cycle bool
	// Kind is what holds itself: Map (an object) or Array.
	Kind Kind
	// At is where the walk stopped, written from the root as JavaScript reads it: self, items[0].owner,
	// ["a b"].c.
	At string
	// Back is, for a cycle, the object or array the walk came back to, written the same way; "" is the
	// root itself.
	Back string
}

func (e *WalkError) Error() string {
	if e.Cycle {
		back := e.Back
		if back == "" {
			back = "gốc"
		}
		return walkNoun(e.Kind) + " tự tham chiếu (vòng ở " + e.At + ", quay về " + back + ")"
	}
	return "giá trị lồng quá sâu (hơn " + strconv.Itoa(MaxDepth) + " tầng object và mảng, ở " + e.At + ")"
}

// Code is the failure code (failure.go): TypeCode for a cycle, JavaScript's TypeError; for a value too
// deep, JavaScript's RangeError, InvalidArgumentCode, an operation past its own bound.
func (e *WalkError) Code() string {
	if e.Cycle {
		return TypeCode
	}
	return InvalidArgumentCode
}

// Failure is the error as a value the program can catch with safe(() => …) (error.code TYPE or
// INVALID_ARGUMENT), its message led by what was walking: "JSON.stringify: object tự tham chiếu (…)".
func (e *WalkError) Failure(what string) Value {
	message := e.Error()
	if what != "" {
		message = what + ": " + message
	}
	return InvalidFailure(e.Code(), message)
}

func walkNoun(kind Kind) string {
	if kind == Array {
		return "mảng"
	}
	return "object"
}

// Path is where a walk outside this package is: Root before walking a value, Key or Index before
// walking into what it holds, Leave after. Key and Index fail on a cycle and past MaxDepth.
type Path struct{ walk walk }

// Root starts the path over at v.
func (p *Path) Root(v Value) {
	p.walk = walk{}
	p.walk.enterValue(v, "", rootStep)
}

// Key puts item, held under key by what the path is at, on the path.
func (p *Path) Key(item Value, key string) *WalkError { return p.walk.enterValue(item, key, keyStep) }

// Index puts item, held at index by what the path is at, on the path.
func (p *Path) Index(item Value, index int) *WalkError {
	return p.walk.enterValue(item, "", index)
}

// Leave takes the last Key or Index off the path.
func (p *Path) Leave() { p.walk.leave() }

const (
	// rootStep is the index of the root of a walk: it is held by nothing.
	rootStep = -2
	// keyStep is the index of a step held under a key.
	keyStep = -1
	// nearSteps is how far a walk searches its path step by step; past it, an index finds a step.
	nearSteps = 32
	// pathSegments bounds how many steps a path written in an error shows, and keyRunes each key.
	pathSegments = 12
	keyRunes     = 40
)

// walkID is what an object or array is, for a walk: two values with the same walkID are one value.
type walkID struct {
	pointer uintptr
	length  int // a slice without its own header is its first element and its length; -1 otherwise
}

type walkStep struct {
	id    walkID // the zero walkID for what can hold nothing
	kind  Kind
	key   string // the key it is held under in the step before it, when index is keyStep
	index int    // its index there, keyStep, or rootStep
}

// walk is the path of one walk: the objects and arrays from the root to where it is.
type walk struct {
	path []walkStep
	far  map[walkID]int // the steps past nearSteps, by what they are, to their place on the path
}

// valueID is what the object or array v is: its map, its slice header, or its first element and length.
func valueID(v Value) walkID {
	switch data := v.V.(type) {
	case map[string]Value:
		if data != nil {
			return walkID{pointer: reflect.ValueOf(data).Pointer(), length: -1}
		}
	case *[]Value:
		if data != nil {
			return walkID{pointer: reflect.ValueOf(data).Pointer(), length: -1}
		}
	case []Value:
		if len(data) > 0 {
			return walkID{pointer: reflect.ValueOf(data).Pointer(), length: len(data)}
		}
	}
	return walkID{}
}

// enterValue puts item — held under key, at index, or the root — on the path.
func (w *walk) enterValue(item Value, key string, index int) *WalkError {
	return w.enter(valueID(item), item.K, key, index)
}

// enter puts what id is on the path, or says why it cannot go there: it is on the path already (a
// cycle), or the path is MaxDepth long.
func (w *walk) enter(id walkID, kind Kind, key string, index int) *WalkError {
	if id != (walkID{}) {
		if back := w.find(id); back >= 0 {
			return &WalkError{Cycle: true, Kind: w.path[back].kind, At: w.text(len(w.path), key, index), Back: w.text(back+1, "", rootStep)}
		}
	}
	if len(w.path) >= MaxDepth {
		return &WalkError{Kind: kind, At: w.text(len(w.path), key, index)}
	}
	if len(w.path) >= nearSteps && id != (walkID{}) {
		if w.far == nil {
			w.far = make(map[walkID]int)
		}
		w.far[id] = len(w.path)
	}
	w.path = append(w.path, walkStep{id: id, kind: kind, key: key, index: index})
	return nil
}

func (w *walk) leave() {
	last := len(w.path) - 1
	if last < 0 {
		return
	}
	if last >= nearSteps && w.path[last].id != (walkID{}) {
		delete(w.far, w.path[last].id)
	}
	w.path = w.path[:last]
}

// find gives where id is on the path, or -1.
func (w *walk) find(id walkID) int {
	near := w.path
	if len(near) > nearSteps {
		near = near[:nearSteps]
	}
	for at := range near {
		if near[at].id == id {
			return at
		}
	}
	if at, ok := w.far[id]; ok {
		return at
	}
	return -1
}

// text writes the path to its step `upTo` (the root not written), then one more step held under key or
// at index (none for rootStep): items[0].owner. A long path keeps its start and its end.
func (w *walk) text(upTo int, key string, index int) string {
	steps := make([]walkStep, 0, upTo+1)
	for _, step := range w.path[:upTo] {
		if step.index != rootStep {
			steps = append(steps, step)
		}
	}
	if index != rootStep {
		steps = append(steps, walkStep{key: key, index: index})
	}
	var out strings.Builder
	if len(steps) > pathSegments {
		head := pathSegments / 3
		for at, step := range steps[:head] {
			writeSegment(&out, step.key, step.index, at == 0)
		}
		out.WriteString("…")
		for _, step := range steps[len(steps)-(pathSegments-head):] {
			writeSegment(&out, step.key, step.index, false)
		}
		return out.String()
	}
	for at, step := range steps {
		writeSegment(&out, step.key, step.index, at == 0)
	}
	return out.String()
}

// writeSegment writes one step of a path as JavaScript reads it: .name, [3], ["a b"].
func writeSegment(out *strings.Builder, key string, index int, first bool) {
	if index >= 0 {
		out.WriteString("[")
		out.WriteString(strconv.Itoa(index))
		out.WriteString("]")
		return
	}
	if utf8.RuneCountInString(key) > keyRunes {
		runes := []rune(key)
		key = string(runes[:keyRunes-1]) + "…"
	}
	if !plainKey(key) {
		out.WriteString("[")
		out.WriteString(strconv.Quote(key))
		out.WriteString("]")
		return
	}
	if !first {
		out.WriteString(".")
	}
	out.WriteString(key)
}

// plainKey reports whether key reads after a dot: a name of letters, digits, _ and $.
func plainKey(key string) bool {
	if key == "" {
		return false
	}
	for at, char := range key {
		switch {
		case char == '_' || char == '$' || unicode.IsLetter(char):
		case unicode.IsDigit(char) && at > 0:
		default:
			return false
		}
	}
	return true
}
