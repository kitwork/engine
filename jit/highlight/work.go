package highlight

// tokenizeWork reads Kitwork's .work language: the JavaScript subset the
// engine's own compiler accepts (engine/compiler), read by the javascript
// scanner (javascript.go) with the subset's words.
//
//   - Keywords are the compiler's: const, let, if, else, for, return, function,
//     new, import, export, switch, void; the words its parser reads by text:
//     case, default, break; of, from, as where an operand follows; and var,
//     the state declaration of views and components (KITWORK_UI §9: `var` is
//     state, `const` is data). The compiler does not list var as a keyword yet
//     (`var x = 1` runs as `var;` then `x = 1`, which stores alike); making it
//     one is the owner's call, but the language already has it, so it reads as
//     one here.
//   - The words the compiler rejects on purpose (while, do, try, catch,
//     finally, throw, class) stay plain, never a keyword or a call: code that
//     uses them does not read as valid. So do words the subset never had
//     (async, await, typeof, continue, this).
//   - Kitwork's lexer has no regular-expression literals, #private names or
//     @decorators, so a '/' always divides and '#' and '@' are plain.
//   - Constants: true, false, null, undefined (undefined is null in the VM).
//   - Types: the capitalised built-ins the VM provides (engine/builtins: Math,
//     JSON, Date, Number, …).
//   - Variables: the names `import { … } from "kitwork"` can bring in, which
//     are the methods of kitwork() (engine/work: router, env, database, log,
//     cache, …) and the ones KITWORK_UI imports for views and components
//     (component, sync, timer); kitwork itself; console (a builtin); app, the
//     manifest's surface; and ctx, the name every handler gives its request
//     context. Once the code declares one of these names itself (const path =
//     …, for (const file of files)), it is a local from there on: plain.
//     Builtin functions (fetch, parseInt) are coloured as calls where called.
func tokenizeWork(scan *scanner) { tokenizeJavaScriptDialect(scan, &dialectWork) }

var dialectWork = javascriptDialectLists{
	keywords: []string{
		"break", "case", "const", "default", "else", "export", "for", "function", "if", "import",
		"let", "new", "return", "switch", "var", "void",
	},
	beforeOperand: []string{"as", "from", "of"},
	reserved:      []string{"catch", "class", "do", "finally", "throw", "try", "while"},
	constants:     []string{"false", "null", "true", "undefined"},
	types:         []string{"Array", "BigInt", "Boolean", "Date", "Error", "JSON", "Math", "Number", "Object", "String"},
	subset:        true,
	hostNames: []string{
		"app", "cache", "collection", "component", "console", "cron", "ctx", "database", "env", "file",
		"http", "insights", "jwt", "kitwork", "log", "napas", "palette", "path", "pigment", "playground",
		"qrcode", "queue", "router", "shortbase", "sqlite", "sync", "timer", "webhook",
	},
}.dialect()
