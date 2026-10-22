package compiler

import (
	"strings"
	"testing"
)

// `import * as router from "kitwork/router"` is the spelling of router.work (19/10/2026): the namespace
// is the object a default import of the same specifier gives, `kitwork().router`.
func TestNamespaceImportLowersLikeDefault(t *testing.T) {
	cases := []struct{ namespace, same string }{
		{`import * as router from "kitwork/router";`, `import router from "kitwork/router";`},
		{`import * as http from "kitwork/http";`, `import http from "kitwork/http";`},
	}
	for _, c := range cases {
		prog, p := parseSrc(c.namespace)
		if len(p.Errors()) > 0 {
			t.Fatalf("%q: unexpected parser errors: %v", c.namespace, p.Errors())
		}
		same, _ := parseSrc(c.same)
		if got, want := prog.String(), same.String(); got != want {
			t.Errorf("%q:\n  got  %q\n  want %q (what %q gives)", c.namespace, got, want, c.same)
		}
	}
	prog, p := parseSrc(`import * as kit from "kitwork";`)
	if len(p.Errors()) > 0 {
		t.Fatalf("bare kitwork: unexpected parser errors: %v", p.Errors())
	}
	if got, want := prog.String(), `const kit = kitwork();`; got != want {
		t.Errorf("bare kitwork:\n  got  %q\n  want %q", got, want)
	}
}

// A namespace import compiles through the whole native path and its name reads the module.
func TestNamespaceImportCompiles(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"app.kitwork.js": `import * as router from "kitwork/router";
router.get((ctx) => ctx.text("x"));`,
	})
	if _, err := CompileFile(entry); err != nil {
		t.Fatalf("compile: %v", err)
	}
}

// What import * cannot be says so, in the author's words, with the way that works.
func TestNamespaceImportErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{`import * from "kitwork/router";`, `import * as router from "kitwork/router"`},
		{`import * as lib from "./lib.kitwork.js";`, `import { a, b } from "./lib.kitwork.js"`},
		{`import * as function from "kitwork/router";`, `import * as tên_khác`},
	}
	for _, c := range cases {
		_, p := parseSrc(c.src)
		joined := strings.Join(p.Errors(), "\n")
		if !strings.Contains(joined, c.want) {
			t.Errorf("%q: errors %q, want one naming %q", c.src, joined, c.want)
		}
		if strings.Contains(joined, "unsupported form") {
			t.Errorf("%q: still the bare %q", c.src, joined)
		}
	}
}

// Two export default in one file is said in the author's words, at the second, with both lines — not
// the internal name __kw_default declared twice at position 0.
func TestSecondExportDefaultNamesBothLines(t *testing.T) {
	src := "export default 1;\nconst a = 2;\nexport default a;\n"
	_, p := parseSrc(src)
	joined := strings.Join(p.Errors(), "\n")
	for _, want := range []string{"3:", "một file chỉ có một export default", "thứ hai ở dòng 3", "lần đầu ở dòng 1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors %q: want %q", joined, want)
		}
	}

	entry := writeTenant(t, map[string]string{"app.kitwork.js": src})
	_, err := CompileFile(entry)
	if err == nil {
		t.Fatal("two export default compiled")
	}
	message := err.Error()
	if strings.Contains(message, DefaultExportName) || strings.Contains(message, "pos 0") {
		t.Errorf("error names the internal binding: %q", message)
	}
	if !strings.Contains(message, "một file chỉ có một export default") || !strings.Contains(message, "dòng 3") {
		t.Errorf("error %q: want the author's words and line 3", message)
	}
}
