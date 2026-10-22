package work

import (
	"net/http"
	"strings"
	"testing"
)

// A method declared twice in one router file is a boot error naming the line of the first, not a quiet
// replacement of it (19/10/2026).
func TestMethodDeclaredTwiceIsBootError(t *testing.T) {
	tests := []struct {
		name   string
		router string
		want   []string
	}{
		{
			name: "router.get twice",
			router: "import { router } from \"kitwork\";\n" +
				"router.get((ctx) => ctx.text(\"first\"));\n" +
				"router.get((ctx) => ctx.text(\"second\"));\n",
			want: []string{"router router.kitwork.js:", "router.get đã khai ở dòng 2", "khai lại ở dòng 3"},
		},
		{
			name: "router.post twice in the namespace spelling",
			router: "import * as router from \"kitwork/router\";\n" +
				"router.get((ctx) => ctx.text(\"page\"));\n" +
				"router.post((ctx) => ctx.text(\"first\"));\n\n" +
				"router.post((ctx) => ctx.text(\"second\")).guard((ctx) => true);\n",
			want: []string{"router.post đã khai ở dòng 3", "khai lại ở dòng 5"},
		},
		{
			name: "router.proxy over router.get",
			router: "import { router } from \"kitwork\";\n" +
				"router.get((ctx) => ctx.text(\"page\"));\n" +
				"router.proxy(\"https://example.com/logo.png\");\n",
			want: []string{"router.proxy ở dòng 3 khai lại GET mà router.get đã khai ở dòng 2"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmp := writeSite(t, map[string]string{"filesystem.kitwork": "", "router.kitwork.js": test.router})
			err := NewTenant(tmp, "localhost").Run()
			if err == nil {
				t.Fatal("boot passed with a method declared twice")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("run error = %v, want substring %q", err, want)
				}
			}
		})
	}
}

// One declaration per method boots: get and post side by side are two methods, not a repeat.
func TestEachMethodOnceBoots(t *testing.T) {
	tmp := writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js": "import { router } from \"kitwork\";\n" +
			"router.get((ctx) => ctx.text(\"page\"));\n" +
			"router.post((ctx) => ctx.text(\"sent\"));\n",
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(tn, "/", nil); r.Code != http.StatusOK || r.Body.String() != "page" {
		t.Fatalf("GET: code=%d body=%q", r.Code, r.Body.String())
	}
}

// `import * as router from "kitwork/router"` names the folder's router itself — the object
// `import router from "kitwork/router"` names — so a route written that way serves.
func TestNamespaceRouterServes(t *testing.T) {
	tmp := writeSite(t, map[string]string{
		"filesystem.kitwork": "",
		"router.kitwork.js":  `import * as router from "kitwork/router";`,
		"hello/router.kitwork.js": "import * as router from \"kitwork/router\";\n" +
			"import same from \"kitwork/router\";\n" +
			"router.get((ctx) => ctx.text(router == same ? \"same router\" : \"another router\"));\n",
	})
	tn := NewTenant(tmp, "localhost")
	if err := tn.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if r := serveGet(tn, "/hello", nil); r.Code != http.StatusOK || r.Body.String() != "same router" {
		t.Fatalf("GET /hello: code=%d body=%q", r.Code, r.Body.String())
	}
}
