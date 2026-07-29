package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// safe() cannot yet rescue a HARD failure, and this pins where the gap actually is so the next
// attempt does not re-diagnose it from scratch.
//
// The value layer is fine: Value{K:Invalid}.Invoke("safe") returns a working SafeResult, and
// navigation.go already lists "safe" as the one key that pierces an Invalid. What stops it is the
// VM. runtime/vm.go checks after EVERY instruction:
//
//	if len(vm.Stack) > 0 && vm.peek().K == value.Invalid { … return }
//
// fail("boom") pushes an Invalid, that check fires on the very next tick, and Run returns before
// the INVOKE carrying .safe() is ever reached. The receiver never survives long enough to be
// rescued.
//
// It matters because Kitwork has no try/catch by design. Until this is closed, a handler cannot
// react to a query that fails outright — the request becomes a 500 rather than a decision the
// author made — and safe() only covers success plus errors ATTACHED to a returned value.
//
// Closing it means choosing an error model, which is a language decision rather than a patch:
//   - defer the halt to the COMMIT boundary, so an Invalid can flow into an INVOKE;
//     changes when errors surface and which line they report;
//   - or compile `X.safe()` to a protected-evaluation opcode, so the guarantee is syntactic — the
//     approach the language already takes with `while` and unbounded loops.
//
// This test asserts TODAY's behaviour. When the gap closes it will fail, which is the point: the
// failure is the reminder to update it rather than a bug report.
func TestSafeDoesNotYetRescueAHardFailure(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(`
import { router } from "kitwork";
router.get((ctx) => ctx.json({ rescued: fail("boom").safe().ok }));
`), 0o644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))

	if rec.Code == http.StatusOK {
		t.Fatalf("safe() now rescues a hard failure — good. Update this test and the note above.\n%s",
			rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("expected the failure to surface with its message, got: %s", rec.Body.String())
	}
}
