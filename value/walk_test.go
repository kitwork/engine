package value

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// A walk over a value — JSON, its text, its Go form, deep equality, flat(), New of Go data — ends in an
// error or a mark when the value holds itself or nests past MaxDepth, never in a Go stack overflow,
// which no recover() catches and which stopped the whole host for one tenant's value (14/10/2026).

// smallStack runs f with Go's stack limited to 64 MiB, a sixteenth of its usual limit: a walk that
// still recursed once per level would overflow it on the deep values these tests build.
func smallStack(t *testing.T, f func()) {
	t.Helper()
	previous := debug.SetMaxStack(64 << 20)
	defer debug.SetMaxStack(previous)
	f()
}

// selfMap is o = { a: 1 }; o.self = o.
func selfMap() Value {
	data := map[string]Value{"a": New(1)}
	object := New(data)
	data["self"] = object
	return object
}

// selfArray is a = [1]; a.push(a).
func selfArray() Value {
	items := []Value{New(1)}
	array := New(&items)
	items = append(items, array)
	return array
}

// nestedBack is a = { b: { c: {} } }; a.b.c.back = a.b — the cycle starts below the root.
func nestedBack() Value {
	c := map[string]Value{}
	b := map[string]Value{"c": New(c)}
	a := map[string]Value{"b": New(b)}
	c["back"] = New(b)
	return New(a)
}

// nestedArrays is depth arrays, one inside the other, around 0: [[[0]]] is 3.
func nestedArrays(depth int) Value {
	v := New(0)
	for i := 0; i < depth; i++ {
		v = New([]Value{v})
	}
	return v
}

func walkErrorOf(t *testing.T, err error) *WalkError {
	t.Helper()
	var walk *WalkError
	if !errors.As(err, &walk) {
		t.Fatalf("error = %v, want a *WalkError", err)
	}
	return walk
}

func TestJSONOfAValueThatHoldsItselfIsATypeError(t *testing.T) {
	cases := []struct {
		label string
		value Value
		want  string
	}{
		{"through a map", selfMap(), "object tự tham chiếu (vòng ở self, quay về gốc)"},
		{"through an array", selfArray(), "mảng tự tham chiếu (vòng ở [1], quay về gốc)"},
		{"through nested maps", nestedBack(), "object tự tham chiếu (vòng ở b.c.back, quay về b)"},
	}
	for _, c := range cases {
		data, err := json.Marshal(c.value)
		if err == nil {
			t.Errorf("%s: json.Marshal = %s, want the cycle error", c.label, data)
			continue
		}
		walk := walkErrorOf(t, err)
		if !walk.Cycle || walk.Error() != c.want || walk.Code() != TypeCode {
			t.Errorf("%s: error = %q (%s), want %q (%s)", c.label, walk.Error(), walk.Code(), c.want, TypeCode)
		}
		failure := walk.Failure("JSON.stringify")
		if got, _ := FailureFrom(failure); failure.K != Invalid || got.Code != TypeCode || got.Message != "JSON.stringify: "+c.want {
			t.Errorf("%s: failure = %+v, want TYPE %q", c.label, got, "JSON.stringify: "+c.want)
		}
		// .json("string") is JSON.stringify by another name
		if got := c.value.ToJson(NewString("string")); got.K != Invalid || !strings.Contains(got.Text(), c.want) {
			t.Errorf("%s: .json(\"string\") = %s, want the failure", c.label, got.Text())
		}
	}
}

// One object reached twice by two paths does not hold itself: it is written twice, as JavaScript does.
func TestJSONWritesASharedObjectTwice(t *testing.T) {
	shared := New(map[string]Value{"n": New(1)})
	pair := New(map[string]Value{"p": shared, "q": shared, "list": New([]Value{shared, shared})})
	data, err := json.Marshal(pair)
	if err != nil || string(data) != `{"list":[{"n":1},{"n":1}],"p":{"n":1},"q":{"n":1}}` {
		t.Fatalf("json.Marshal = %s, %v", data, err)
	}
}

// 10000 levels are what JSON.parse reads: they are written back, and read again. One more is a
// RangeError naming where; a hundred thousand end the same way, not in Go's stack.
func TestJSONNestsToMaxDepthAndNoFurther(t *testing.T) {
	smallStack(t, func() {
		data, err := json.Marshal(nestedArrays(MaxDepth))
		if err != nil {
			t.Fatalf("depth %d: %v", MaxDepth, err)
		}
		if want := strings.Repeat("[", MaxDepth) + "0" + strings.Repeat("]", MaxDepth); string(data) != want {
			t.Fatalf("depth %d: wrong JSON (%d bytes)", MaxDepth, len(data))
		}
		var back Value
		if err := json.Unmarshal(data, &back); err != nil || !back.Equal(nestedArrays(MaxDepth)) {
			t.Fatalf("depth %d does not read back: %v", MaxDepth, err)
		}

		for _, depth := range []int{MaxDepth + 1, 100_000} {
			_, err := json.Marshal(nestedArrays(depth))
			walk := walkErrorOf(t, err)
			if walk.Cycle || walk.Code() != InvalidArgumentCode {
				t.Fatalf("depth %d: %v, want the RangeError", depth, walk)
			}
			want := "giá trị lồng quá sâu (hơn 10000 tầng object và mảng, ở [0][0][0][0]…[0][0][0][0][0][0][0][0])"
			if walk.Error() != want {
				t.Fatalf("depth %d: %q, want %q", depth, walk.Error(), want)
			}
		}
	})
}

// legacyJSON is how a Value was written before the walk: json.Marshal of its storage, every value
// inside through MarshalJSON again. The walk must write every value that has an end exactly so.
type legacyJSON struct{ v Value }

func (l legacyJSON) MarshalJSON() ([]byte, error) {
	switch l.v.K {
	case Array:
		switch data := l.v.V.(type) {
		case *[]Value:
			if data == nil || *data == nil {
				return []byte("null"), nil
			}
			return json.Marshal(legacyItems(*data))
		case []Value:
			if data == nil {
				return []byte("null"), nil
			}
			return json.Marshal(legacyItems(data))
		}
		return json.Marshal(l.v.V)
	case Map:
		data, ok := l.v.V.(map[string]Value)
		if !ok {
			return json.Marshal(l.v.V)
		}
		if data == nil {
			return []byte("null"), nil
		}
		out := make(map[string]legacyJSON, len(data))
		for key, item := range data {
			out[key] = legacyJSON{item}
		}
		return json.Marshal(out)
	}
	return l.v.MarshalJSON()
}

func legacyItems(items []Value) []legacyJSON {
	out := make([]legacyJSON, len(items))
	for i, item := range items {
		out[i] = legacyJSON{item}
	}
	return out
}

func jsonCorpus() []Value {
	nilItems := []Value(nil)
	texts := []string{"", "plain", `a"b`, `a\b`, "<b>&amp;</b>", "tab\there", "line\nbreak", "\x01\x1f\x7f",
		"Phường", "\xff\xfe", "a b c", "emoji 😀", "</script>"}
	numbers := []float64{0, math.Copysign(0, -1), 1, -1, 1.5, -2.25, 1e21, 1e20, 1e-7, 1e-6, 123456789012,
		9007199254740993, 1e300, 0.1, 100}
	var corpus []Value
	for _, text := range texts {
		corpus = append(corpus, NewString(text), New(map[string]Value{text: NewString(text)}))
	}
	for _, number := range numbers {
		corpus = append(corpus, New(number), New([]Value{New(number)}))
	}
	corpus = append(corpus,
		NewNil(), TRUE, FALSE, Value{}, Value{K: Invalid, V: "boom"},
		New(time.Date(2026, 10, 14, 9, 30, 0, 5, time.UTC)), New(90*time.Second),
		New([]byte("bytes <&>")), NewFunc(func(...Value) Value { return NewNil() }),
		Value{K: Proxy}, Value{K: Struct, V: struct{ A int }{1}},
		Value{K: Array}, Value{K: Array, V: (*[]Value)(nil)}, Value{K: Array, V: &nilItems},
		Value{K: Array, V: []Value{New(1), NewString("<x>")}}, Value{K: Array, V: []Value(nil)},
		Value{K: Map}, Value{K: Map, V: map[string]Value(nil)}, Value{K: Map, V: map[string]any{"z": 1, "a": "<"}},
		New([]Value{}), New(map[string]Value{}),
		New(map[string]Value{
			"list":  New([]Value{New(1), NewString("<two>"), NewNil(), New(map[string]Value{"deep": New([]Value{TRUE})})}),
			"fn":    NewFunc(func(...Value) Value { return NewNil() }),
			"b":     New(map[string]Value{"&": NewString("x"), "": New(0)}),
			"empty": New([]Value{}),
			"when":  New(time.Unix(1_700_000_000, 0)),
		}),
	)
	random := rand.New(rand.NewSource(14))
	for i := 0; i < 200; i++ {
		corpus = append(corpus, randomValue(random, 4))
	}
	return corpus
}

func randomValue(random *rand.Rand, depth int) Value {
	choice := random.Intn(9)
	if depth == 0 {
		choice %= 5
	}
	switch choice {
	case 0:
		return New(random.NormFloat64() * math.Pow(10, float64(random.Intn(30)-10)))
	case 1:
		return New(random.Intn(2000) - 1000)
	case 2:
		runes := []rune("ab<>&\"\\\n é😀 ")
		var text strings.Builder
		for n := random.Intn(6); n > 0; n-- {
			text.WriteRune(runes[random.Intn(len(runes))])
		}
		return NewString(text.String())
	case 3:
		return NewBool(random.Intn(2) == 0)
	case 4:
		return NewNil()
	case 5, 6:
		items := make([]Value, random.Intn(4))
		for i := range items {
			items[i] = randomValue(random, depth-1)
		}
		return New(items)
	default:
		object := map[string]Value{}
		for n := random.Intn(4); n > 0; n-- {
			key := string(rune('a'+random.Intn(26))) + []string{"", "<", " x", "é"}[random.Intn(4)]
			object[key] = randomValue(random, depth-1)
		}
		return New(object)
	}
}

func TestJSONWritesEveryValueAsBefore(t *testing.T) {
	for i, item := range jsonCorpus() {
		got, gotErr := json.Marshal(item)
		want, wantErr := json.Marshal(legacyJSON{item})
		if (gotErr != nil) != (wantErr != nil) || string(got) != string(want) {
			t.Errorf("value %d (%s): got %s, %v; before %s, %v", i, item.K, got, gotErr, want, wantErr)
		}
	}
	// NaN has no JSON, inside an object as at the top
	if _, err := json.Marshal(New(map[string]Value{"x": New(math.NaN())})); err == nil {
		t.Error("NaN inside an object was written")
	}
}

func TestTextMarksAValueThatHoldsItself(t *testing.T) {
	if got := selfMap().Text(); got != "{a: 1, self: [tự tham chiếu]}" && got != "{self: [tự tham chiếu], a: 1}" {
		t.Errorf("text of o.self = o: %q", got)
	}
	if got := selfArray().Text(); got != "[1, [tự tham chiếu]]" {
		t.Errorf("text of a.push(a): %q", got)
	}
	if got := nestedBack().Text(); got != "{b: {c: {back: [tự tham chiếu]}}}" {
		t.Errorf("text of a.b.c.back = a.b: %q", got)
	}
	shared := New([]Value{New(1)})
	if got := New([]Value{shared, shared}).Text(); got != "[[1], [1]]" {
		t.Errorf("text of a shared array: %q", got)
	}
	smallStack(t, func() {
		if got := nestedArrays(MaxDepth).Text(); got != strings.Repeat("[", MaxDepth)+"0"+strings.Repeat("]", MaxDepth) {
			t.Errorf("text of %d levels is not whole (%d bytes)", MaxDepth, len(got))
		}
		got := nestedArrays(100_000).Text()
		if want := strings.Repeat("[", MaxDepth) + "[quá sâu]" + strings.Repeat("]", MaxDepth); got != want {
			t.Errorf("text of 100000 levels: %d bytes, want %d levels then [quá sâu]", len(got), MaxDepth)
		}
	})
}

func TestInterfaceLeavesNilWhereAValueHoldsItself(t *testing.T) {
	plain, ok := selfMap().Interface().(map[string]any)
	if !ok || plain["a"] != float64(1) || plain["self"] != nil || len(plain) != 2 {
		t.Errorf("Interface of o.self = o: %#v", plain)
	}
	list, ok := selfArray().Interface().([]any)
	if !ok || len(list) != 2 || list[0] != float64(1) || list[1] != nil {
		t.Errorf("Interface of a.push(a): %#v", list)
	}
	if _, err := json.Marshal(nestedBack().Interface()); err != nil {
		t.Errorf("Interface of a.b.c.back = a.b still holds itself: %v", err)
	}
	smallStack(t, func() {
		current := nestedArrays(100_000).Interface()
		levels := 0
		for {
			items, ok := current.([]any)
			if !ok {
				break
			}
			levels++
			current = items[0]
		}
		if levels != MaxDepth || current != nil {
			t.Errorf("Interface of 100000 levels: %d levels then %#v, want %d then nil", levels, current, MaxDepth)
		}
	})
}

// Equality waits its pairs on a list, not on Go's stack: values nested half a million deep compare.
func TestEqualityOfDeepValues(t *testing.T) {
	const depth = 500_000
	smallStack(t, func() {
		if !nestedArrays(depth).Equal(nestedArrays(depth)) {
			t.Error("two equal deep values were not equal")
		}
		left := New(map[string]Value{"x": nestedArrays(depth)})
		right := New(map[string]Value{"x": New([]Value{nestedArrays(depth - 1), New(1)})})
		if left.Equal(right) {
			t.Error("two different deep values were equal")
		}
	})
	if !selfMap().Equal(selfMap()) || selfMap().Equal(selfArray()) || !nestedBack().Equal(nestedBack()) {
		t.Error("values that hold themselves do not compare")
	}
}

func TestFlatOfAnArrayThatHoldsItselfIsATypeError(t *testing.T) {
	for _, depth := range []Value{New(1), New(math.Inf(1)), New(1e9)} {
		got := selfArray().ArrayFlat(depth)
		failure, _ := FailureFrom(got)
		if got.K != Invalid || failure.Code != TypeCode ||
			failure.Message != "flat: mảng tự tham chiếu (vòng ở [1], quay về gốc)" {
			t.Errorf("a.flat(%s) = %s (%s)", depth.Text(), got.Text(), failure.Code)
		}
	}
	// flat(Infinity) spreads every level, as JavaScript's does; one too many is a RangeError
	smallStack(t, func() {
		if got := nestedArrays(MaxDepth - 1).ArrayFlat(New(math.Inf(1))); got.Text() != "[0]" {
			t.Errorf("flat(Infinity) of %d levels = %s", MaxDepth-1, got.Text())
		}
		got := nestedArrays(100_000).ArrayFlat(New(math.Inf(1)))
		if failure, _ := FailureFrom(got); got.K != Invalid || failure.Code != InvalidArgumentCode {
			t.Errorf("flat(Infinity) of 100000 levels = %s", got.Text())
		}
	})
	// as before: a depth, a shared array, NaN and a negative depth (no spreading)
	shared := New([]Value{New(1)})
	if got := New([]Value{shared, shared, New([]Value{New([]Value{New(2)})})}).ArrayFlat(); got.Text() != "[1, 1, [2]]" {
		t.Errorf("flat() = %s", got.Text())
	}
	for _, depth := range []float64{math.NaN(), -1, 0} {
		if got := New([]Value{New([]Value{New(1)})}).ArrayFlat(New(depth)); got.Text() != "[[1]]" {
			t.Errorf("flat(%v) = %s", depth, got.Text())
		}
	}
}

// New of Go data that holds itself — a .ui page's {{ x = $ }} handed to a helper — is the failure.
func TestNewOfGoDataThatHoldsItselfIsATypeError(t *testing.T) {
	scope := map[string]any{"a": 1.0}
	scope["x"] = scope
	list := []any{1.0, nil}
	list[1] = list
	type box [1]any
	boxed := &box{}
	boxed[0] = boxed
	nested := map[string]any{"b": map[string]any{}}
	nested["b"].(map[string]any)["back"] = nested["b"]
	cases := []struct {
		label string
		data  any
		want  string
	}{
		{"a map in itself", scope, "object tự tham chiếu (vòng ở x, quay về gốc)"},
		{"a slice in itself", list, "mảng tự tham chiếu (vòng ở [1], quay về gốc)"},
		{"an array through its pointer", boxed, "mảng tự tham chiếu (vòng ở [0], quay về gốc)"},
		{"nested maps", nested, "object tự tham chiếu (vòng ở b.back, quay về b)"},
	}
	for _, c := range cases {
		got := New(c.data)
		failure, _ := FailureFrom(got)
		if got.K != Invalid || failure.Code != TypeCode || failure.Message != c.want {
			t.Errorf("%s: New = %s (%s), want %q", c.label, got.Text(), failure.Code, c.want)
		}
	}

	// the text of an object writes its keys sorted (14/10/2026): one text, read once. Until then the
	// check accepted either order but called Text() twice, and two calls could walk the map in two
	// orders, so it failed now and then whatever the text was.
	shared := map[string]any{"n": 1.0}
	if got := New(map[string]any{"p": shared, "q": []any{shared, shared}}).Text(); got != "{p: {n: 1}, q: [{n: 1}, {n: 1}]}" {
		t.Errorf("New of shared Go data = %s", got)
	}
	smallStack(t, func() {
		var deep any = 0.0
		for i := 0; i < 100_000; i++ {
			deep = []any{deep}
		}
		got := New(deep)
		if failure, _ := FailureFrom(got); got.K != Invalid || failure.Code != InvalidArgumentCode {
			t.Errorf("New of 100000 levels = %.80s", got.Text())
		}
	})
}

type walkNode struct {
	Name string    `json:"name"`
	Next *walkNode `json:"next"`
}

// To follows a Go type that holds itself as deep as the value goes: past MaxDepth it fails.
func TestToFailsPastMaxDepth(t *testing.T) {
	data := map[string]Value{"name": NewString("loop")}
	node := New(data)
	data["next"] = node
	var target walkNode
	smallStack(t, func() {
		if err := node.To(&target); err == nil || !strings.Contains(err.Error(), "lồng quá sâu") {
			t.Fatalf("To of a value that holds itself = %v, want the depth error", err)
		}
	})
	var short walkNode
	if err := New(map[string]Value{"name": NewString("a"), "next": New(map[string]Value{"name": NewString("b")})}).To(&short); err != nil ||
		short.Next == nil || short.Next.Name != "b" || short.Next.Next != nil {
		t.Fatalf("To of a short list = %+v, %v", short, err)
	}
}

// Path is the walk for code outside the package (the .ui converter).
func TestPathFindsACycleAndKeepsSharedObjects(t *testing.T) {
	root := selfMap()
	var path Path
	path.Root(root)
	err := path.Key(root.Map()["self"], "self")
	if err == nil || err.Error() != "object tự tham chiếu (vòng ở self, quay về gốc)" {
		t.Fatalf("Key(self) = %v", err)
	}
	shared := New([]Value{New(1)})
	path.Root(New([]Value{shared, shared}))
	if err := path.Index(shared, 0); err != nil {
		t.Fatalf("Index(0) = %v", err)
	}
	path.Leave()
	if err := path.Index(shared, 1); err != nil {
		t.Fatalf("Index(1) of a shared array = %v", err)
	}
	if !reflect.DeepEqual(path.walk.text(len(path.walk.path), "k y", keyStep), `[1]["k y"]`) {
		t.Fatalf("path text = %s", path.walk.text(len(path.walk.path), "k y", keyStep))
	}
}
