package value

import "testing"

func TestUnshiftOwnsItsResultStorage(t *testing.T) {
	target := New([]Value{})
	args := []Value{NewString("kept")}

	target.Unshift(args...)
	args[0] = NewString("overwritten")

	items := target.Array()
	if len(items) != 1 || items[0].Text() != "kept" {
		t.Fatalf("unshift retained caller argument storage: %#v", items)
	}
}

func TestStandardMethodResolverExcludesExtensions(t *testing.T) {
	if _, ok := Array.StandardMethod("push"); !ok {
		t.Fatal("array push is not registered as a standard method")
	}
	if _, ok := Array.StandardMethod("not-a-standard-method"); ok {
		t.Fatal("unknown method was reported as standard")
	}
}
