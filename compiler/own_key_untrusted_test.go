package compiler

import (
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/value"
)

// Data read from outside (a request body, a database row, JSON.parse) is an object too. A key it
// carries is data when READ (body.pick is what the sender put there), but data never answers a CALL
// (13/10/2026, "khoá riêng là hàm mới gọi nó"): body.pick("name"), body.omit, body.has, body.keys,
// body.text and body.json stay the builtins whatever keys the body brings, so whitelisting through
// body.pick cannot be bypassed by a body that carries a "pick" key. Until that rule this body made
// body.pick("name") hand back the role field.
func TestUntrustedObjectCannotReplaceABuiltinCall(t *testing.T) {
	globals := map[string]value.Value{}
	builtins.InjectJSCompat(globals)

	hostile := `const body = JSON.parse('{"name":"a","role":"admin","pick":{"name":"a","role":"admin"},"omit":{"role":"admin"},"has":true,"keys":["x"],"text":"T","json":{"j":1}}')
`
	// The calls are the builtins: pick keeps only name, has reports the real keys, keys lists them.
	calls := hostile + `const result = [body.pick("name"), body.has("token"), body.has("role"), body.keys().length, body.omit("pick", "omit", "has", "keys", "text", "json", "role")]`
	if got, want := runOwnKey(t, calls, globals), `[{"name":"a"},false,true,8,{"name":"a"}]`; got != want {
		t.Errorf("builtins called on a body that carries their names = %s, want %s", got, want)
	}

	// Through a bracket the data, called, is not a function: a runtime error (14/10/2026; until then
	// null), never the data either.
	bracket := hostile + `const p = safe(() => body["pick"]("name"))
const h = safe(() => body["has"]("token"))
const result = [p.value, p.error.message, p.error.at, h.value, h.error.message, h.error.at]`
	if got, want := runOwnKey(t, bracket, globals), `[null,"body[\"pick\"] không phải hàm (là object): không gọi được","\u003csource\u003e:2:22",null,"body[\"has\"] không phải hàm (là boolean true): không gọi được","\u003csource\u003e:3:22"]`; got != want {
		t.Errorf("bracket calls on the same body = %s, want %s", got, want)
	}

	// Reading the keys still gives what the sender wrote.
	read := hostile + `const result = [body.pick, body.has, body.keys, body.text, body["pick"].role]`
	if got, want := runOwnKey(t, read, globals), `[{"name":"a","role":"admin"},true,["x"],"T","admin"]`; got != want {
		t.Errorf("reading the same keys = %s, want the data %s", got, want)
	}

	// Read, an object's own length, error and isError are what the data says (13/10/2026, "sửa: khoá
	// riêng thắng"), so a body can claim any .length and any error. The count the engine keeps is a
	// call, which data cannot answer: body.len(), body.length() and Object.keys(body).length are the
	// real number of keys, and an object that says it has a length is still not an array. The error
	// the engine reports is the one safe(() => …) gives, never a key of the data.
	counted := `const body = JSON.parse('{"items":{"length":1000000000000},"length":7,"error":"E","isError":true,"a":1}')
const checked = safe(() => body)
const result = [body.length, body.error, body.isError, body.len(), body.length(), Object.keys(body).length, Array.isArray(body.items), body.items.len(), checked.error, checked.value.error]`
	if got, want := runOwnKey(t, counted, globals), `[7,"E",true,5,5,5,false,1,null,"E"]`; got != want {
		t.Errorf("reading and counting a body that claims a length = %s, want %s", got, want)
	}

	// A field named like a builtin that the data left out reads as the builtin, a function, so it is
	// truthy: if (!body.text) cannot catch it. body.has("text"), which data cannot replace, can.
	missing := `const body = JSON.parse('{"a":1}')
const result = [body.text ? "truthy" : "falsy", body.has("text"), Object.keys(body).includes("text")]`
	if got, want := runOwnKey(t, missing, globals), `["truthy",false,false]`; got != want {
		t.Errorf("a missing text field = %s, want %s", got, want)
	}
}

// An import lowers to destructuring the module's exports object (const { has } = module), so an
// export named after a builtin goes through the own-key rule: has, text and keys arrive as the module
// exported them (before 13/10/2026 they arrived as the builtin methods of an object), and so do error
// and length, which the engine answered before the module's own key until the same day (error arrived
// as null, length as the number of exports).
func TestImportedNameOfABuiltinIsTheExport(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"helper.kitwork.js": "export const has = (x) => \"own-has:\" + x\n" +
			"export const text = \"own-text\"\n" +
			"export const keys = 5\n" +
			"export const error = (message) => \"error:\" + message\n" +
			"export const length = 99\n" +
			"export const isError = \"own-isError\"\n",
		"app.kitwork.js": "import { has, text, keys, error, length, isError } from \"./helper.kitwork.js\"\n" +
			"const result = [[has(1), text, keys], [error(\"x\"), length, isError]]\n",
	})
	bytecode, err := CompileFile(entry)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got, want := runCompiledResult(t, bytecode, nil, entry), `[["own-has:1","own-text",5],["error:x",99,"own-isError"]]`; got != want {
		t.Fatalf("imports = %s, want %s", got, want)
	}
}
