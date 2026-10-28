package value

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type navigationReflectFixture struct {
	Name   string
	hidden string
}

type navigationFluentError struct{}

func (*navigationFluentError) Error() string { return "" }

func (fixture *navigationReflectFixture) Ready() bool {
	return fixture != nil && fixture.Name != ""
}

func (fixture *navigationReflectFixture) Echo(message string) string {
	return fixture.Name + ":" + message
}

func (fixture *navigationReflectFixture) Pair() (string, error) {
	return fixture.Name, nil
}

func (fixture *navigationReflectFixture) Check() error {
	if fixture.Name == "" {
		return errors.New("no name")
	}
	return nil
}

// A Go method of no argument that returns only an error is read as a field, and the read keeps the error:
// it fails with it, as a call does (14/10/2026; napas.validate() showed it until napas went on 28/10/2026).
// CONTROL: with a name the read gives nothing.
func TestReflectedGetterKeepsItsError(t *testing.T) {
	if got := New(&navigationReflectFixture{}).Get("check"); got.K != Invalid || !strings.Contains(got.Text(), "call check: no name") {
		t.Fatalf("a getter that returns only an error dropped it: %#v", got)
	}
	if got := New(&navigationReflectFixture{Name: "kitwork"}).Get("check"); got.K != Nil {
		t.Fatalf("a getter that returns only a nil error = %#v", got)
	}
}

func TestStructReflectionMetadataCachePreservesSemantics(t *testing.T) {
	item := New(&navigationReflectFixture{Name: "kitwork", hidden: "private"})

	for iteration := 0; iteration < 100; iteration++ {
		if got := item.Get("name").String(); got != "kitwork" {
			t.Fatalf("field = %q", got)
		}
		if !item.Get("READY").Truthy() {
			t.Fatal("zero-argument getter was not invoked case-insensitively")
		}
		echo := item.Get("echo")
		if echo.K != Func {
			t.Fatalf("method kind = %s", echo.K)
		}
		if got := echo.Call("echo", NewString("vm")).String(); got != "kitwork:vm" {
			t.Fatalf("method result = %q", got)
		}
		if got := item.Get("hidden"); got.K != Nil {
			t.Fatalf("unexported field became visible: %#v", got)
		}
		if got := item.Get("pair"); got.K != Func {
			t.Fatalf("multi-result method was invoked as a getter: %#v", got)
		}
	}
}

func TestReflectedCallValidatesArgumentsAndErrors(t *testing.T) {
	add := Value{
		K: Func,
		V: reflect.ValueOf(func(left, right int) int {
			return left + right
		}),
	}
	if got := add.Call("add", New(20), New(22)); got.Int() != 42 {
		t.Fatalf("valid call result = %#v", got)
	}
	if got := add.Call("add", New(20)); got.K != Invalid ||
		!strings.Contains(got.Text(), "expected at least 2") {
		t.Fatalf("too-few result = %#v", got)
	}
	if got := add.Call("add", New("twenty"), New(22)); got.K != Invalid ||
		!strings.Contains(got.Text(), "cannot convert") {
		t.Fatalf("incompatible result = %#v", got)
	}

	hostFailure := Value{
		K: Func,
		V: reflect.ValueOf(func() (string, error) {
			return "", errors.New("host failure")
		}),
	}
	if got := hostFailure.Call("load"); got.K != Invalid ||
		!strings.Contains(got.Text(), "host failure") {
		t.Fatalf("host error result = %#v", got)
	}

	fluent := Value{
		K: Func,
		V: reflect.ValueOf(func() *navigationFluentError {
			return &navigationFluentError{}
		}),
	}
	if got := fluent.Call("fluent"); got.K != Struct {
		t.Fatalf("ordinary object implementing error was misclassified: %#v", got)
	}
}

func BenchmarkStructReflectionMemberLookup(b *testing.B) {
	item := New(&navigationReflectFixture{Name: "kitwork"})
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		benchmarkResult = item.Get("echo")
	}
}

var benchmarkResult Value

type navigationBoundFixture struct{ name string }

func (fixture *navigationBoundFixture) Echo(message string) string {
	return fixture.name + ":" + message
}

// An object of a type BindMethods names gives a method read without its call as a BoundMethod that names
// the object and the method, and calling it calls the method; a call after a dot runs the method as
// before, and an object of another type still gives the Go method value.
//
// CONTROL: with reflect never binding, the read gave the Go method value ("read = reflect.Value").
func TestBoundMethodNamesItsObject(t *testing.T) {
	BindMethods((*navigationBoundFixture)(nil))
	object := New(&navigationBoundFixture{name: "a"})
	read := object.Get("echo")
	bound, ok := read.V.(*BoundMethod)
	if read.K != Func || !ok {
		t.Fatalf("read = %T, want a *BoundMethod", read.V)
	}
	if bound.Object.V != object.V || bound.Name != "echo" {
		t.Fatalf("bound = %+v, want the object and echo", bound)
	}
	if called := read.Call("echo", New("x")); called.Text() != "a:x" {
		t.Fatalf("calling the bound method gave %s", called.Text())
	}
	if invoked := object.Invoke("echo", New("y")); invoked.Text() != "a:y" {
		t.Fatalf("invoke gave %s", invoked.Text())
	}
	if other := New(&navigationReflectFixture{Name: "b"}).Get("echo"); other.K != Func {
		t.Fatalf("another type's method read = %v", other)
	} else if _, isBound := other.V.(*BoundMethod); isBound {
		t.Fatal("a type BindMethods did not name gave a BoundMethod")
	}
}
