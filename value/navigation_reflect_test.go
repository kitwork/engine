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
