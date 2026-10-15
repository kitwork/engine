package work

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/compiler"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// A request's nested runs (compute.go: query-builder lambdas, transaction callbacks) draw on the
// request VM's energy and call depth, and a failure that ends a run ends the request, whatever the
// query builder does with the lambda's result (runtime/nesting.go). Each test has a control: the
// same route with room to finish, which proves the fixture and makes the failure the budget's.

const nestingPreamble = `
import { router, sqlite } from "kitwork";

const burn = (n) => {
    let total = 0;
    for (let i = 0; i < n; i++) {
        total = total + i;
    }
    return total;
};
`

func newNestingTenant(t *testing.T, routes string, maxEnergy uint64) *Tenant {
	t.Helper()
	saved := AllowLocal
	AllowLocal = true // developing: a failure answers with its text, which the tests read
	t.Cleanup(func() { AllowLocal = saved })
	root := t.TempDir()
	routeDir := filepath.Join(root, "acme", "localhost", "nest")
	if err := os.MkdirAll(routeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := nestingPreamble + routes
	if err := os.WriteFile(filepath.Join(routeDir, "router.kitwork.js"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tenant.Close)
	tenant.MaxEnergy = maxEnergy
	return tenant
}

func serveNesting(t *testing.T, tenant *Tenant, requestContext context.Context) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://localhost/nest", nil).WithContext(requestContext)
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, request)
	return recorder.Body.String()
}

// Query-builder lambdas in a loop: each burns about 72 000, under the 300 000 ceiling, and the loop
// alone is far under it; twenty of them are not. The request fails on energy instead of every
// lambda starting with a fresh 300 000.
func TestQueryLambdasShareTheRequestEnergy(t *testing.T) {
	routes := `
router.get((ctx) => {
    let rounds = 0;
    for (let k = 0; k < 20; k++) {
        sqlite.table("items").where(row => {
            burn(2000);
            return row.id == 1;
        });
        rounds = rounds + 1;
    }
    return ctx.text("rounds:" + rounds);
});
`
	if body := serveNesting(t, newNestingTenant(t, routes, 10_000_000), context.Background()); !strings.Contains(body, "rounds:20") {
		t.Fatalf("control: with room, all twenty rounds run: %q", body)
	}

	body := serveNesting(t, newNestingTenant(t, routes, 300_000), context.Background())
	if strings.Contains(body, "rounds:") || !strings.Contains(body, "Energy Limit Exceeded") {
		t.Fatalf("the lambdas together exceed the request's energy; the request must fail on it: %q", body)
	}
}

// A transaction callback and the query lambda inside it, nested two deep: each round spends about
// 144 000 across both, the ceiling is 300 000, so the third round cannot finish. The inner lambda
// draws on the callback, the callback on the request.
func TestNestedTransactionsShareTheRequestEnergy(t *testing.T) {
	routes := `
router.get((ctx) => {
    sqlite.exec("CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT)");
    let rounds = 0;
    for (let k = 0; k < 10; k++) {
        sqlite.atomic((tx) => {
            burn(2000);
            tx.table("items").where(row => {
                burn(2000);
                return row.id == 1;
            });
        });
        rounds = rounds + 1;
    }
    return ctx.text("rounds:" + rounds);
});
`
	if body := serveNesting(t, newNestingTenant(t, routes, 10_000_000), context.Background()); !strings.Contains(body, "rounds:10") {
		t.Fatalf("control: with room, all ten transactions run: %q", body)
	}

	body := serveNesting(t, newNestingTenant(t, routes, 300_000), context.Background())
	if strings.Contains(body, "rounds:") || !strings.Contains(body, "Energy Limit Exceeded") {
		t.Fatalf("the nested callbacks together exceed the request's energy; the request must fail on it: %q", body)
	}
}

// Recursion through query lambdas: every level is a nested VM, so it is Go stack as well as frames.
// The levels share the request's 64 frames and the request fails with a stack overflow; with a frame
// budget of its own each level would go on (here to 40; without a bound, until the process dies).
func TestNestedLambdasShareTheFrameCeiling(t *testing.T) {
	routes := `
const dig = (level, limit) => {
    let reached = level;
    if (level < limit) {
        sqlite.table("items").where(row => {
            reached = dig(level + 1, limit);
            return row.id == 1;
        });
    }
    return reached;
};

router.get((ctx) => ctx.text("deepest:" + dig(1, LIMIT)));
`
	shallow := strings.Replace(routes, "LIMIT", "10", 1)
	if body := serveNesting(t, newNestingTenant(t, shallow, 10_000_000), context.Background()); !strings.Contains(body, "deepest:10") {
		t.Fatalf("control: ten levels fit in 64 frames: %q", body)
	}

	deep := strings.Replace(routes, "LIMIT", "40", 1)
	body := serveNesting(t, newNestingTenant(t, deep, 10_000_000), context.Background())
	if strings.Contains(body, "deepest:") || !strings.Contains(body, "Stack overflow") {
		t.Fatalf("forty levels do not fit in the request's 64 frames: %q", body)
	}
}

// Cancelling the request while a query lambda runs stops the lambda (it shares the request's context)
// and ends the handler as the query call returns: the handler never writes "after", although the
// query builder ignores the lambda's result.
func TestCancellationInsideQueryLambdaEndsTheRequest(t *testing.T) {
	routes := `
router.get((ctx) => {
    sqlite.table("items").where(row => {
        cancelRequest();
        burn(100000);
        return row.id == 1;
    });
    return ctx.text("after");
});
`
	control := newNestingTenant(t, routes, 100_000_000)
	control.vm.Globals["cancelRequest"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Nil}
	})
	if body := serveNesting(t, control, context.Background()); !strings.Contains(body, "after") {
		t.Fatalf("control: uncancelled, the handler runs on: %q", body)
	}

	tenant := newNestingTenant(t, routes, 100_000_000)
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	tenant.vm.Globals["cancelRequest"] = value.NewFunc(func(...value.Value) value.Value {
		cancel()
		return value.Value{K: value.Nil}
	})
	body := serveNesting(t, tenant, requestContext)
	if strings.Contains(body, "after") || !strings.Contains(body, "Cancelled") {
		t.Fatalf("the request was cancelled inside the lambda; the handler must stop there: %q", body)
	}
}

// A Go panic inside a query lambda is NATIVE_PANIC for the request: safe(() => …) does not catch it and
// the handler does not go on, as with a panic in the handler's own host call.
func TestPanicInsideQueryLambdaIsNativePanic(t *testing.T) {
	routes := `
router.get((ctx) => {
    const attempt = safe(() => sqlite.table("items").where(row => {
        hostCall();
        return row.id == 1;
    }));
    return ctx.text("after");
});
`
	control := newNestingTenant(t, routes, 10_000_000)
	control.vm.Globals["hostCall"] = value.NewFunc(func(...value.Value) value.Value {
		return value.Value{K: value.Nil}
	})
	if body := serveNesting(t, control, context.Background()); !strings.Contains(body, "after") {
		t.Fatalf("control: without the panic, the handler runs on: %q", body)
	}

	tenant := newNestingTenant(t, routes, 10_000_000)
	tenant.vm.Globals["hostCall"] = value.NewFunc(func(...value.Value) value.Value {
		panic("host call exploded")
	})
	body := serveNesting(t, tenant, context.Background())
	if strings.Contains(body, "after") || !strings.Contains(body, "Native panic") {
		t.Fatalf("the panic must end the request as NATIVE_PANIC: %q", body)
	}
}

// The capability compute seam (Tenant.Execute) nests too, as capabilities/database runs an atomic
// callback through it: each callback fits the ceiling, twenty do not, and safe(() => …) cannot catch
// the energy limit although the capability turns the failure into a plain value. The callback reaches the
// host as a function argument (CALL), the query lambdas above as a method argument (INVOKE).
func TestExecuteSeamSharesTheRequestEnergy(t *testing.T) {
	routes := `
router.get((ctx) => {
    let rounds = 0;
    for (let k = 0; k < 20; k++) {
        const attempt = safe(() => atomically(() => burn(2000)));
        rounds = rounds + 1;
    }
    return ctx.text("rounds:" + rounds);
});
`
	serve := func(t *testing.T, maxEnergy uint64) string {
		tenant := newNestingTenant(t, routes, maxEnergy)
		tenant.vm.Globals["atomically"] = value.NewFunc(func(args ...value.Value) value.Value {
			lambda, _ := args[0].V.(*value.Lambda)
			if _, err := tenant.Execute(nil, lambda, nil); err != nil {
				return value.Value{K: value.Invalid, V: err.Error()}
			}
			return value.Value{K: value.Nil}
		})
		return serveNesting(t, tenant, context.Background())
	}

	if body := serve(t, 10_000_000); !strings.Contains(body, "rounds:20") {
		t.Fatalf("control: with room, all twenty callbacks run: %q", body)
	}
	body := serve(t, 300_000)
	if strings.Contains(body, "rounds:") || !strings.Contains(body, "Energy Limit Exceeded") {
		t.Fatalf("the callbacks together exceed the request's energy; the request must fail on it: %q", body)
	}
}

// nestingLambda is a lambda with bytecode of its own, for running the executor directly.
func nestingLambda(t *testing.T) (*runtime.Program, *value.Lambda) {
	t.Helper()
	bytecode, err := compiler.CompileSource(`var f = () => 1;`)
	if err != nil {
		t.Fatal(err)
	}
	vm := runtime.New(bytecode.Program)
	if result := vm.Run(); result.K == value.Invalid {
		t.Fatal(result.Text())
	}
	lambda, _ := vm.Vars["f"].V.(*value.Lambda)
	if lambda == nil {
		t.Fatal("f is not a function")
	}
	return bytecode.Program, lambda
}

// A panic in compute.go itself, before the lambda runs (here preparing a VM for a tenant that has
// none), is NATIVE_PANIC, not a plain value safe(() => …) would catch; the same in Tenant.Execute. The
// control runs the same lambda for a tenant that is ready.
func TestComputePanicIsNativePanic(t *testing.T) {
	program, lambda := nestingLambda(t)

	ready := newNestingTenant(t, `router.get((ctx) => ctx.text("ready"));`, 10_000_000)
	if result := (tenantLambdaExecutor{tenant: ready}).ExecuteLambda(lambda, nil); result.K != value.Number || result.N != 1 {
		t.Fatalf("control: the lambda runs for a ready tenant: %#v", result)
	}

	broken := &Tenant{}
	result := (tenantLambdaExecutor{tenant: broken}).ExecuteLambda(lambda, nil)
	diagnostic, ok := runtime.DiagnosticFrom(result)
	if !ok || diagnostic.Code != runtime.DiagnosticNativePanic {
		t.Fatalf("a panic in the executor must be NATIVE_PANIC: %#v", result)
	}
	if !strings.Contains(diagnostic.Message, "nested execution") {
		t.Fatalf("message = %q", diagnostic.Message)
	}

	_, runErr := broken.Execute(program, lambda, nil)
	var executeDiagnostic *runtime.Diagnostic
	if !errors.As(runErr, &executeDiagnostic) || executeDiagnostic.Code != runtime.DiagnosticNativePanic {
		t.Fatalf("a panic in Execute must be NATIVE_PANIC: %v", runErr)
	}
}

// Even a panic before the nested run began reaches the request's VM: the request's next host call
// fails with it. The control is the same request without the panic.
func TestComputePanicReachesTheRequestVM(t *testing.T) {
	_, lambda := nestingLambda(t)
	ready := newNestingTenant(t, `router.get((ctx) => ctx.text("ready"));`, 10_000_000)

	run := func(t *testing.T, tenant *Tenant) value.Value {
		t.Helper()
		scope := requestscope.New(nil, nil, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		defer scope.Close()
		requestVM, err := scope.LeaseVM(enginePool.Acquire, enginePool.Release)
		if err != nil {
			t.Fatal(err)
		}
		bytecode, err := compiler.CompileSource(`var x = nested();`)
		if err != nil {
			t.Fatal(err)
		}
		requestVM.FastReset(bytecode.Program, map[string]value.Value{
			"nested": value.NewFunc(func(...value.Value) value.Value {
				(tenantLambdaExecutor{tenant: tenant, requestScope: scope}).ExecuteLambda(lambda, nil)
				return value.Value{K: value.Nil}
			}),
		})
		requestVM.MaxEnergy = 10_000_000
		return requestVM.Run()
	}

	if result := run(t, ready); result.K == value.Invalid {
		t.Fatalf("control: a nested run that succeeds leaves the request running: %s", result.Text())
	}
	result := run(t, &Tenant{})
	if diagnostic, ok := runtime.DiagnosticFrom(result); !ok || diagnostic.Code != runtime.DiagnosticNativePanic {
		t.Fatalf("the executor's panic must end the request as NATIVE_PANIC: %#v", result)
	}
}
