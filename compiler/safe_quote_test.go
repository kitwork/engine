package compiler

import (
	"strings"
	"testing"
)

// .safe() written after ?. is the removed method as much as after a plain dot (14/10/2026): o?.safe()
// and o.safe?.() are the compile error that quotes the new spelling. They compiled, and failed at run
// time (o.safe is not a function) or quietly gave null, so the error the design calls the detector of
// what a migration missed did not find them. CONTROL: with an argument it is an ordinary method call.
func TestRemovedSafeMethodAfterOptionalChain(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const o = { a: 1 };\nconst r = o?.safe();", "<source>:2:14: .safe() đã bỏ (14/10/2026): viết safe(() => o), nhận { value, error }"},
		{"const o = { a: 1 };\nconst r = o.safe?.();", "<source>:2:13: .safe() đã bỏ (14/10/2026): viết safe(() => o), nhận { value, error }"},
		{"const r = { safe: () => 9 }?.safe();", "viết safe(() => ({ safe: () => 9 })), nhận { value, error }"},
		{"const r = db.users?.first()?.safe();", "viết safe(() => db.users?.first()), nhận { value, error }"},
	} {
		_, err := CompileSource(c.source)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q = %v\nwant %s", c.source, err, c.want)
		}
	}
	if got, want := runOwnKey(t, "const o = { safe: (x) => \"own:\" + x }\nconst result = [o?.safe(1), o.safe?.(2)]", nil), `["own:1","own:2"]`; got != want {
		t.Fatalf("CONTROL: own safe with an argument = %s, want %s", got, want)
	}
}

// The fix a message quotes is a copy that means what the code means: a string keeps its spaces and
// escapes as written, and a comment is left out. oneLine collapsed spaces inside strings ("a  b"
// became "a b") and pulled a // comment onto the line, where it hid the rest of the copy.
func TestQuotedFixKeepsStringsAndDropsComments(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const r = db.where(\"a  b\", \"x ,y\", \"( z\").safe();", `viết safe(() => db.where("a  b", "x ,y", "( z")), nhận`},
		{"const r = db.users // tất cả\n  .list().safe();", "viết safe(() => db.users.list()), nhận"},
		{"const r = f('it\\'s  ok', `t  ${x}`).safe();", "viết safe(() => f('it\\'s  ok', `t  ${x}`)), nhận"},
		{"const r = safe(f( // c\n 1));", "safe nhận một hàm: viết safe(() => f(1))"},
		{"const r = safe(f(\"a   b\"));", `safe nhận một hàm: viết safe(() => f("a   b"))`},
	} {
		_, err := CompileSource(c.source)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q = %v\nwant %s", c.source, err, c.want)
		}
	}
	// CONTROL: the breaks of a chain written one call a line still go away
	if got := oneLine("db.users\n    .where(\"id\", 1)\n    .first()"); got != `db.users.where("id", 1).first()` {
		t.Fatalf("CONTROL: oneLine of a chain = %q", got)
	}
}
