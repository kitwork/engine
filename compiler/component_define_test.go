package compiler

import (
	"path/filepath"
	"testing"
)

// A component of components.work is readied for the bridge as router.view's function is (28/10/2026): a var at
// the top of its function reads what the instance holds through its context, and the object it binds gains
// __state. A function of the file that is no component's is left as written.
func TestComponentDefineIsReadiedForTheBridge(t *testing.T) {
	prog, err := parseProgramSource(`import * as component from "kitwork/component";
const other = (context) => { var kept = 1; return context.bind({ kept }); };
component.define("shop-cart", (context) => {
  var items = [];
  var done = false;
  const checkout = () => { items = []; done = true; };
  return context.bind({ items, done, checkout });
});
`, "components.work")
	if err != nil {
		t.Fatal(err)
	}
	rewriteComponentDefines(prog)
	var fn *FunctionLiteral
	for _, statement := range prog.Statements {
		if line, ok := statement.(*ExpressionStatement); ok {
			if found := componentDefineFunction(line.Expression); found != nil {
				fn = found
			}
		}
	}
	if fn == nil {
		t.Fatal("component.define(\"shop-cart\", …) not found")
	}
	read := map[string]bool{}
	for _, statement := range fn.Body.Statements {
		line, ok := statement.(*ExpressionStatement)
		if !ok {
			continue
		}
		if assign, ok := line.Expression.(*AssignmentExpression); ok {
			call, ok := assign.Value.(*MethodCallExpression)
			name, _ := assign.Name.(*Identifier)
			if ok && name != nil && call.Method.Value == "viewVar" && isNamed(call.Object, "context") {
				read[name.Value] = true
			}
		}
	}
	if !read["items"] || !read["done"] {
		t.Errorf("the vars should read context.viewVar: %v", read)
	}
	object := boundObject(fn.Body.Statements[len(fn.Body.Statements)-1].(*ReturnStatement).ReturnValue)
	last := object.Entries[len(object.Entries)-1]
	if key, _ := last.Key.(*Literal); key == nil || key.Value.Text() != ViewStateName {
		t.Errorf("the bound object should end with %s, got %v", ViewStateName, last.Key)
	}
	// other is no component's: its var stays as written
	other := prog.Statements[1].(*VarStatement).Value.(*FunctionLiteral)
	for _, statement := range other.Body.Statements {
		if line, ok := statement.(*ExpressionStatement); ok {
			if assign, ok := line.Expression.(*AssignmentExpression); ok {
				if _, rewritten := assign.Value.(*MethodCallExpression); rewritten {
					t.Error("a function that is no component's should not be rewritten")
				}
			}
		}
	}
}

// CompileComponentFile compiles components.work with its components readied; CompileModuleFileInApp, the same
// file left as written, does not (the control).
func TestCompileComponentFileReadiesItsComponents(t *testing.T) {
	root := writeApps(t, map[string]string{
		"apps/acme/site/components.work": "import * as component from \"kitwork/component\";\n" +
			"component.define(\"shop-cart\", (context) => { var items = []; return context.bind({ items }); });\n",
	})
	app, file := filepath.Join(root, "apps", "acme"), filepath.Join(root, "apps", "acme", "site", "components.work")
	readied, err := CompileComponentFile(app, file)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := CompileModuleFileInApp(app, file)
	if err != nil {
		t.Fatal(err)
	}
	if !hasConstant(readied, "viewVar") {
		t.Error("CompileComponentFile should read a var through context.viewVar")
	}
	if hasConstant(plain, "viewVar") {
		t.Error("CompileModuleFileInApp should leave the file as written")
	}
}

func isNamed(e Expression, name string) bool {
	id, ok := e.(*Identifier)
	return ok && id.Value == name
}

// hasConstant reports whether a compiled program holds the string text among its constants.
func hasConstant(bytecode *Bytecode, text string) bool {
	for _, constant := range bytecode.Program.Constants() {
		if constant.IsString() && constant.Text() == text {
			return true
		}
	}
	return false
}
