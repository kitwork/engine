package compiler

import (
	"strings"
	"testing"
)

// A request described and dropped as a statement is a compile error (22/10/2026): until then
// http.get(url); sent the request at the end of the statement, and .send() is now the only place a
// request goes out, so the statement would send nothing without a word. The error is at the module's
// name and writes the chain to use, with the name the import gave the module; a method imported by name
// (import { get } from "kitwork/http") or taken from the module by a const is checked the same way.
func TestUnsentRequestIsACompileError(t *testing.T) {
	const say = "request chưa gửi: .send() là chỗ duy nhất gửi request, viết "
	for _, c := range []struct{ source, want string }{
		{"import * as http from \"kitwork/http\";\nhttp.get(\"https://api.example.test/feed\");",
			"<source>:2:1: " + say + "http.get(url).send()"},
		// modifiers do not send either
		{"import * as http from \"kitwork/http\";\nhttp.post(\"/orders\").header(\"Accept\", \"application/json\").timeout(\"5s\");",
			"<source>:2:1: " + say + "http.post(url).send()"},
		// inside a handler, through the bare "kitwork" import
		{"import { http } from \"kitwork\";\nconst handle = (context) => {\n  http.delete(\"/orders/1\");\n  return context.json({});\n};",
			"<source>:3:3: " + say + "http.delete(url).send()"},
		// the name the import gave the module
		{"import { http as client } from \"kitwork\";\nclient.put(\"/x\").retry(1);",
			"<source>:2:1: " + say + "client.put(url).send()"},
		{"import web from \"kitwork/http\";\nweb.head(\"/x\");",
			"<source>:2:1: " + say + "web.head(url).send()"},
		// a method imported by name, or taken from the module by a const, is checked as the module is
		{"import { get } from \"kitwork/http\";\nget(\"https://api.example.test/feed\");",
			"<source>:2:1: " + say + "get(url).send()"},
		{"import { post as create, put } from \"kitwork/http\";\ncreate(\"/orders\").timeout(\"5s\");",
			"<source>:2:1: " + say + "create(url).send()"},
		{"import { http } from \"kitwork\";\nconst { get } = http;\nget(\"/x\");",
			"<source>:3:1: " + say + "get(url).send()"},
		{"import * as http from \"kitwork/http\";\nconst { delete: remove } = http;\nremove(\"/orders/1\").retry(1);",
			"<source>:3:1: " + say + "remove(url).send()"},
		{"import * as http from \"kitwork/http\";\nconst patch = http.patch;\npatch(\"/x\");",
			"<source>:3:1: " + say + "patch(url).send()"},
		{"import * as http from \"kitwork/http\";\nconst client = http;\nclient.options(\"/x\");",
			"<source>:3:1: " + say + "client.options(url).send()"},
	} {
		_, err := CompileSource(c.source)
		if err == nil {
			t.Errorf("%q compiled", c.source)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q:\n got %v\nwant %s", c.source, err, c.want)
		}
	}

	// control: a request sent, kept for later, returned, or a name that is not the module compiles
	for _, source := range []string{
		"import * as http from \"kitwork/http\";\nhttp.get(\"/x\").send();",
		"import * as http from \"kitwork/http\";\nhttp.get(\"/x\").timeout(\"5s\").send().json();",
		"import * as http from \"kitwork/http\";\nconst feed = http.get(\"/x\").timeout(\"5s\");",
		"import * as http from \"kitwork/http\";\nconst describe = (path) => http.get(path);",
		"import * as http from \"kitwork/http\";\nconst f = () => {\n  const http = { get: (path) => path };\n  http.get(\"/x\");\n};",
		"const http = { get: (path) => path };\nhttp.get(\"/x\");",
		"import * as router from \"kitwork/router\";\nrouter.get((context) => context.json({}));",
		"import { get } from \"kitwork/http\";\nget(\"/x\").send();",
		"import { get } from \"kitwork/http\";\nconst feed = get(\"/x\").timeout(\"5s\");",
		"import * as http from \"kitwork/http\";\nconst { get } = http;\nget(\"/x\").query({ page: 1 }).send();",
		"import { get } from \"kitwork/router\";\nget((context) => context.json({}));",
		"import * as http from \"kitwork/http\";\nconst { send } = http;\nconst headers = http.headers;",
	} {
		if _, err := CompileSource(source); err != nil {
			t.Errorf("%q: %v, want it to compile", source, err)
		}
	}
}
