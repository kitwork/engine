package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func TestRuntimeDiagnosticPreservesNamedCallStack(t *testing.T) {
	source := `const inner = () => {
	failHost()
}
const outer = () => {
	inner()
}
outer()`

	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile source: %v", err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Globals["failHost"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "host failed"}
	})

	result := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no structured diagnostic: %#v", result)
	}
	if diagnostic.Code != runtime.DiagnosticRuntimeError {
		t.Fatalf("diagnostic code = %q, want %q", diagnostic.Code, runtime.DiagnosticRuntimeError)
	}

	gotNames := make([]string, len(diagnostic.Stack))
	gotLines := make([]int32, len(diagnostic.Stack))
	for index, frame := range diagnostic.Stack {
		gotNames[index] = frame.Function
		gotLines[index] = frame.Line
	}
	if want := []string{"inner", "outer", "<main>"}; !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("stack functions = %#v, want %#v", gotNames, want)
	}
	if want := []int32{2, 5, 7}; !reflect.DeepEqual(gotLines, want) {
		t.Fatalf("stack lines = %#v, want %#v", gotLines, want)
	}
	if diagnostic.Function != "inner" || diagnostic.Line != 2 {
		t.Fatalf("top location = %s:%d, want inner:2", diagnostic.Function, diagnostic.Line)
	}
	if diagnostic.File != "<source>" || diagnostic.Column != 2 {
		t.Fatalf(
			"top source = %s:%d:%d, want <source>:2:2",
			diagnostic.File,
			diagnostic.Line,
			diagnostic.Column,
		)
	}
	if text := result.Text(); !strings.Contains(text, "host failed (at line 2)") ||
		!strings.Contains(text, "at outer (<source>:5:2") {
		t.Fatalf("formatted diagnostic = %q", text)
	}
}

func TestRuntimeDiagnosticNamesAnonymousCallback(t *testing.T) {
	source := `const run = (callback) => callback()
run(() => failHost())`

	bytecode, err := CompileSource(source)
	if err != nil {
		t.Fatalf("compile source: %v", err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Globals["failHost"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "callback failed"}
	})

	result := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no structured diagnostic: %#v", result)
	}

	gotNames := make([]string, len(diagnostic.Stack))
	for index, frame := range diagnostic.Stack {
		gotNames[index] = frame.Function
	}
	if want := []string{"<anonymous>", "run", "<main>"}; !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("stack functions = %#v, want %#v", gotNames, want)
	}
}

func TestCompilerInfersObjectFunctionName(t *testing.T) {
	bytecode, err := CompileSource(`const handlers = { save: () => null }`)
	if err != nil {
		t.Fatalf("compile source: %v", err)
	}

	for _, constant := range bytecode.Constants() {
		if function, ok := constant.V.(*value.Lambda); ok {
			if function.Name != "save" {
				t.Fatalf("function name = %q, want save", function.Name)
			}
			return
		}
	}
	t.Fatal("compiled constants contain no lambda")
}

func TestArrayCallbackPropagatesStructuredDiagnostic(t *testing.T) {
	bytecode, err := CompileSource(`[1].map(() => failHost())`)
	if err != nil {
		t.Fatalf("compile source: %v", err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Globals["failHost"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "map callback failed"}
	})

	result := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no structured diagnostic: %#v", result)
	}
	gotNames := make([]string, len(diagnostic.Stack))
	for index, frame := range diagnostic.Stack {
		gotNames[index] = frame.Function
	}
	if want := []string{"<anonymous>", "<main>"}; !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("stack functions = %#v, want %#v", gotNames, want)
	}
}

func TestArraySortComparatorReturnsNumber(t *testing.T) {
	bytecode, err := CompileSource(`
const items = [{ count: 1 }, { count: 3 }, { count: 2 }]
items.sort((a, b) => b.count - a.count)
const result = items.map((item) => item.count)
`)
	if err != nil {
		t.Fatalf("compile source: %v", err)
	}
	vm := runtime.New(bytecode.Program)

	result := vm.Run()
	if result.K == value.Invalid {
		t.Fatalf("sort result = %s", result.Text())
	}
	got := vm.Vars["result"].Interface()
	want := []any{float64(3), float64(2), float64(1)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted values = %#v, want %#v", got, want)
	}
}

func TestRecursiveCallReturnsStackOverflowDiagnostic(t *testing.T) {
	bytecode, err := CompileSource(`const recurse = () => recurse()
recurse()`)
	if err != nil {
		t.Fatalf("compile source: %v", err)
	}
	vm := runtime.New(bytecode.Program)

	result := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no structured diagnostic: %#v", result)
	}
	if diagnostic.Code != runtime.DiagnosticStackOverflow {
		t.Fatalf("diagnostic code = %q, want %q", diagnostic.Code, runtime.DiagnosticStackOverflow)
	}
	if diagnostic.Function != "recurse" {
		t.Fatalf("top function = %q, want recurse", diagnostic.Function)
	}
	if len(diagnostic.Stack) != len(vm.Frames) {
		t.Fatalf("stack depth = %d, want VM limit %d", len(diagnostic.Stack), len(vm.Frames))
	}
}

func TestNativeImportDiagnosticPreservesSourceFiles(t *testing.T) {
	entry := writeTenant(t, map[string]string{
		"app.kitwork.js": `import { outer } from "./lib/outer.kitwork.js";
outer()`,
		"lib/outer.kitwork.js": `import { inner } from "./inner.kitwork.js";
export const outer = () => inner()`,
		"lib/inner.kitwork.js": `export const inner = () => {
	failHost()
}`,
	})

	bytecode, err := CompileFile(entry)
	if err != nil {
		t.Fatalf("compile native source graph: %v", err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Globals["failHost"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Invalid, V: "native module failed"}
	})

	result := vm.Run()
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok {
		t.Fatalf("result has no structured diagnostic: %#v", result)
	}

	gotNames := make([]string, len(diagnostic.Stack))
	gotFiles := make([]string, len(diagnostic.Stack))
	for index, frame := range diagnostic.Stack {
		gotNames[index] = frame.Function
		gotFiles[index] = frame.File
	}
	if want := []string{"inner", "outer", "<main>"}; !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("stack functions = %#v, want %#v", gotNames, want)
	}
	if want := []string{
		"lib/inner.kitwork.js",
		"lib/outer.kitwork.js",
		"app.kitwork.js",
	}; !reflect.DeepEqual(gotFiles, want) {
		t.Fatalf("stack files = %#v, want %#v", gotFiles, want)
	}
	if !strings.Contains(
		result.Text(),
		"at inner (lib/inner.kitwork.js:2:2",
	) {
		t.Fatalf("formatted diagnostic = %q", result.Text())
	}
}
