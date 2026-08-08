package hydrate

import "testing"

// The compiled IR must match, byte-for-byte, what the client interpreter (hydrate.v1.js) expects.
func TestCompileJSON(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"n", `["$","n"]`},
		{"n = n + 1", `["=","n",["+",["$","n"],["#",1]]]`},
		{"n = n - 1", `["=","n",["-",["$","n"],["#",1]]]`},
		{"n > 3", `[">",["$","n"],["#",3]]`},
		{"n * qty", `["*",["$","n"],["$","qty"]]`},
		{"(n * qty).toFixed(2)", `["()",["*",["$","n"],["$","qty"]],"toFixed",[["#",2]]]`},
		{"!open", `["u!",["$","open"]]`},
		{"a && b || c", `["||",["&&",["$","a"],["$","b"]],["$","c"]]`},
		// UTF-8 (Vietnamese + emoji) must survive verbatim, operators must stay literal (no HTML escaping):
		{"name ? 'Chào ' + name + ' 👋' : 'x'", `["?",["$","name"],["+",["+",["#","Chào "],["$","name"]],["#"," 👋"]],["#","x"]]`},
		// $ = the page scope: readable as a member, writable ONLY through the $.name form.
		{"$.total", `[".",["$","$"],"total"]`},
		{"$.total = $.total + 1", `["=$","total",["+",[".",["$","$"],"total"],["#",1]]]`},
		{"n > $.limit", `[">",["$","n"],[".",["$","$"],"limit"]]`},
		// blueprint grammar — object / array / arrow / sequence / bare-call, all compiled (no eval):
		{"{ count: 5, open: false }", `["{}",[["count",["#",5]],["open",["#",false]]]]`},
		{"{ count: 5, }", `["{}",[["count",["#",5]]]]`}, // objects allow a trailing comma
		{"[1, 'a', qty]", `["[]",[["#",1],["#","a"],["$","qty"]]]`},
		{"() => count = count + 1", `["=>",[],["=","count",["+",["$","count"],["#",1]]]]`},
		{"(x, y) => x + y", `["=>",["x","y"],["+",["$","x"],["$","y"]]]`},
		{"a = 1; b = a + 1", `[";",["=","a",["#",1]],["=","b",["+",["$","a"],["#",1]]]]`},
		{"inc()", `["call",["$","inc"],[]]`},
		{"add(1, 2)", `["call",["$","add"],[["#",1],["#",2]]]`},
		{"{ count: 5, inc: () => count = count + 1 }", `["{}",[["count",["#",5]],["inc",["=>",[],["=","count",["+",["$","count"],["#",1]]]]]]]`},
		// regressions: the arrow lookahead must NOT swallow a plain parenthesized expression,
		// and the method-call path must survive next to bare-call.
		{"(a + b) * c", `["*",["+",["$","a"],["$","b"]],["$","c"]]`},
		{"(qty * price).toFixed(2)", `["()",["*",["$","qty"],["$","price"]],"toFixed",[["#",2]]]`},
	}
	for _, c := range cases {
		got, err := CompileJSON(c.expr)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.expr, err)
		}
		if got != c.want {
			t.Errorf("Compile(%q)\n got:  %s\n want: %s", c.expr, got, c.want)
		}
	}
}

func TestCompilePrecedence(t *testing.T) {
	// 1 + 2 * 3  →  1 + (2*3), not (1+2)*3
	got, err := CompileJSON("1 + 2 * 3")
	if err != nil {
		t.Fatal(err)
	}
	if want := `["+",["#",1],["*",["#",2],["#",3]]]`; got != want {
		t.Errorf("precedence\n got:  %s\n want: %s", got, want)
	}
}

func TestCompileErrors(t *testing.T) {
	// a.b = 1 stays out of the grammar ($.name is the ONLY assignable member); $ itself is read-only;
	// arrays reject a trailing comma; malformed objects/arrows are rejected.
	for _, e := range []string{"n +", "1 2", ")", "* 3", "n = ", "a.b = 1", "$ = 5",
		"[1, 2,]", "{ count 5 }", "(x, 1) => x", "{ 5: 1 }",
		"n@", "1.2.3", "'unterminated"} {
		if _, err := CompileJSON(e); err == nil {
			t.Errorf("expected error for %q, got none", e)
		}
	}
}

// `kit` service calls and `$app` component calls are ordinary member expressions. Compilation proves
// grammar validity; runtime capability availability remains a separate platform concern.
func TestCompileKitServicesAndAppComponent(t *testing.T) {
	for _, src := range []string{
		"kit.theme.toggle()",
		"kit.theme.set('dark')",
		"kit.theme.mode",
		"kit.theme.resolved",
		"kit.clipboard.writeText('npm i kitwork')",
		"kit.clipboard.writeText(bill_id)",
		"kit.clipboard.readText()",
		"kit.clipboard.copy(bill_id)",
		"kit.camera.capture()",
		"kit.navigation.back()",
		"kit.navigation.forward()",
		"kit.navigation.reload()",
		"kit.window.minimize()",
		"kit.window.maximize()",
		"kit.window.restore()",
		"kit.window.close()",
		"kit.capabilities.supports('camera.capture')",
		"kit.storage.get('theme')",
		"kit.dialog.confirm({ message: 'Continue?' })",
		"kit.share.open({ title: 'Kitwork' })",
		"$app.captureAvatar()",
		"{ src: avatar, alt: name, disabled: n > 3 }",
	} {
		if _, err := Compile(src); err != nil {
			t.Errorf("capability expression must compile: %s → %v", src, err)
		}
	}
}
