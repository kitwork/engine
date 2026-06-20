package compiler

import "testing"

func parseSrc(src string) (*Program, *Parser) {
	l := NewLexer(src)
	p := NewParser(l)
	return p.ParseProgram(), p
}

// Native import/export must lower to existing AST nodes (no esbuild).
func TestNativeImportLowering(t *testing.T) {
	cases := []struct{ src, want string }{
		{`import { router, log } from "kitwork";`, `const { router, log } = kitwork();`},
		{`import { router } from "kitwork/router";`, `const { router } = kitwork();`},
		{`import http from "kitwork/http";`, `const http = kitwork().http;`},
		{`export const x = 5;`, `const x = 5;`},
	}
	for _, c := range cases {
		prog, p := parseSrc(c.src)
		if len(p.Errors()) > 0 {
			t.Fatalf("%q: unexpected parser errors: %v", c.src, p.Errors())
		}
		if got := prog.String(); got != c.want {
			t.Errorf("%q:\n  got  %q\n  want %q", c.src, got, c.want)
		}
	}
}

// `export default <expr>` lowers to `const __kw_default = <expr>` (keeps the
// side effect; the bundler exposes it under the "default" key) and flags Program.
func TestExportDefaultLowersToConst(t *testing.T) {
	prog, p := parseSrc(`export default router.get("/x");`)
	if len(p.Errors()) > 0 {
		t.Fatalf("unexpected parser errors: %v", p.Errors())
	}
	if len(prog.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(prog.Statements))
	}
	vs, ok := prog.Statements[0].(*VarStatement)
	if !ok {
		t.Fatalf("expected *VarStatement, got %T", prog.Statements[0])
	}
	if len(vs.Names) != 1 || vs.Names[0].Value != DefaultExportName {
		t.Fatalf("expected binding to %q, got %+v", DefaultExportName, vs.Names)
	}
	if !prog.HasDefault {
		t.Fatalf("expected Program.HasDefault = true")
	}
}

// Relative imports are now NATIVE: they parse into *ImportStatement nodes that
// the script bundler resolves (IIFE-wrap), NOT a parser error.
func TestRelativeImportIsNativeNode(t *testing.T) {
	cases := []struct {
		src        string
		wantNames  int
		wantDef    bool
		wantSideFx bool
	}{
		{`import { greet } from "./lib/greet.kitwork.js";`, 1, false, false},
		{`import helper from "../helper.kitwork.js";`, 0, true, false},
		{`import "./routes/hello.kitwork.js";`, 0, false, true},
	}
	for _, c := range cases {
		prog, p := parseSrc(c.src)
		if len(p.Errors()) > 0 {
			t.Fatalf("%q: unexpected parser errors: %v", c.src, p.Errors())
		}
		imp, ok := prog.Statements[0].(*ImportStatement)
		if !ok {
			t.Fatalf("%q: expected *ImportStatement, got %T", c.src, prog.Statements[0])
		}
		if len(imp.Names) != c.wantNames || (imp.Default != nil) != c.wantDef || imp.SideEffect != c.wantSideFx {
			t.Errorf("%q: got names=%d default=%v sidefx=%v", c.src, len(imp.Names), imp.Default != nil, imp.SideEffect)
		}
	}
}

// `as` aliases are now NATIVE: kitwork aliases lower to a GroupStatement of
// `const local = kitwork().imported` member bindings.
func TestKitworkAliasLowersToMemberConsts(t *testing.T) {
	prog, p := parseSrc(`import { router as r, log as l } from "kitwork";`)
	if len(p.Errors()) > 0 {
		t.Fatalf("unexpected errors: %v", p.Errors())
	}
	grp, ok := prog.Statements[0].(*GroupStatement)
	if !ok {
		t.Fatalf("expected *GroupStatement, got %T", prog.Statements[0])
	}
	if len(grp.Statements) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(grp.Statements))
	}
	for i, want := range []string{"r", "l"} {
		vs, ok := grp.Statements[i].(*VarStatement)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Value != want {
			t.Fatalf("binding %d: want `const %s = …`, got %#v", i, want, grp.Statements[i])
		}
		if _, ok := vs.Value.(*MemberExpression); !ok {
			t.Fatalf("binding %d: expected MemberExpression value", i)
		}
	}
}

// Relative aliases ride along on the ImportStatement spec (imported vs local).
func TestRelativeAliasCarriesSpec(t *testing.T) {
	prog, p := parseSrc(`import { greet as g } from "./lib/x.kitwork.js";`)
	if len(p.Errors()) > 0 {
		t.Fatalf("unexpected errors: %v", p.Errors())
	}
	imp, ok := prog.Statements[0].(*ImportStatement)
	if !ok {
		t.Fatalf("expected *ImportStatement, got %T", prog.Statements[0])
	}
	if len(imp.Names) != 1 || imp.Names[0].Imported != "greet" || imp.Names[0].Local != "g" {
		t.Fatalf("got %#v", imp.Names)
	}
}

// Kitwork has no npm/node_modules: a bare non-kitwork, non-relative specifier is
// invalid and must be rejected (no esbuild to fall back to anymore).
func TestBarePackageRejected(t *testing.T) {
	_, p := parseSrc(`import pkg from "some-package";`)
	if len(p.Errors()) == 0 {
		t.Errorf("expected a parser error for bare non-kitwork package")
	}
}

// The lowered nodes must actually compile to bytecode (end to end, no esbuild).
func TestNativeImportCompiles(t *testing.T) {
	src := `import { router, log } from "kitwork"; const x = router;`
	prog, p := parseSrc(src)
	if len(p.Errors()) > 0 {
		t.Fatalf("parse errors: %v", p.Errors())
	}
	c := NewCompiler(src)
	if err := c.Compile(prog); err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if c.ByteCodeResult() == nil {
		t.Fatalf("nil bytecode")
	}
}
