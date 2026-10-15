package database_test

import (
	"database/sql"
	"testing"

	dbcap "github.com/kitwork/engine/capabilities/database"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
	_ "modernc.org/sqlite"
)

// runtimeScope is a scope that runs a lambda as the host's Execute seam does (work.Tenant.Execute): on
// a VM of its program, handing a failed run back as its *runtime.Diagnostic.
type runtimeScope struct{ mockScope }

func (s *runtimeScope) Execute(program *runtime.Program, fn *value.Lambda, args []value.Value) (uint64, error) {
	if program == nil {
		program, _ = runtime.ProgramFromRef(fn.Program)
	}
	vm := runtime.New(program)
	if result := vm.ExecuteLambda(fn, args); result.K == value.Invalid {
		if diagnostic, ok := runtime.DiagnosticFrom(result); ok {
			return vm.Energy, diagnostic
		}
	}
	return vm.Energy, nil
}

// An atomic callback that fails is the callback's own failure (14/10/2026): safe(() => …) reads its
// message, code and place, as work's db.atomic gives them. It was a failure made of the diagnostic's
// text, the message led by the place and followed by the stack, its at the call of atomic. CONTROL: a
// callback that works commits and gives no error.
func TestAtomicCallbackFailureKeepsItsMessageAndPlace(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	adapter := dbcap.NewDatabaseAdapter(&runtimeScope{mockScope{db: db}})

	bytecode, err := compiler.CompileSource("const failed = safe(() => atomic(() => {\n  const o = { x: 3 };\n  o.x(1);\n}));\nconst kept = safe(() => atomic(() => 1));\nvar message = failed.error.message;\nvar code = failed.error.code;\nvar at = failed.error.at;\nvar keptError = kept.error;")
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	vm.Globals["safe"] = value.NewSafeFunction()
	vm.Globals["atomic"] = value.NewFunc(adapter.Atomic)
	if result := vm.Run(); result.K == value.Invalid {
		t.Fatalf("run = %s", result.Text())
	}
	if message, code, at := vm.Vars["message"].Text(), vm.Vars["code"].Text(), vm.Vars["at"].Text(); message != "o.x không phải hàm (là số): không gọi được" ||
		code != value.NotAFunctionCode || at != "<source>:3:3" {
		t.Fatalf("error = { message: %q, code: %s, at: %s }, want the callback's NOT_A_FUNCTION at <source>:3:3", message, code, at)
	}
	if kept := vm.Vars["keptError"]; kept.K != value.Nil {
		t.Fatalf("CONTROL: a callback that works gave error %s", kept.Text())
	}
}
