package env_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/capabilities"
	_ "github.com/kitwork/engine/capabilities/env"
	"github.com/kitwork/engine/value"
)

type mockScope struct {
	root string
}

func (m *mockScope) AppID() string  { return "app_test" }
func (m *mockScope) Domain() string { return "test.com" }
func (m *mockScope) ResolvePath(paths ...string) string {
	if len(paths) == 0 {
		return m.root
	}
	return filepath.Join(append([]string{m.root}, paths...)...)
}
func (m *mockScope) DB(name string) *sql.DB { return nil }

func TestEnvCapability(t *testing.T) {
	if got := capabilities.DefaultRegistry.GetLifetime("env"); got != capabilities.LifetimeSite {
		t.Fatalf("env lifetime = %v, want LifetimeSite", got)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("PORT=8080\nDEBUG=true\n"), 0644)

	scope := &mockScope{root: dir}
	val, ok := capabilities.DefaultRegistry.Get("env", scope)
	if !ok {
		t.Fatal("Env capability not registered")
	}

	if val.K != value.Proxy {
		t.Fatalf("Expected Proxy value, got %v", val.K)
	}

	port := val.Invoke("require", value.NewString("PORT"))
	if port.N != 8080 {
		t.Errorf("Expected PORT=8080, got %v", port.N)
	}

	// env has one method, require: env.DEBUG() calls a value, the not-a-function error (14/10/2026).
	called := val.Invoke("DEBUG")
	if message, ok := value.NotAFunctionFrom(called); !ok || message != "DEBUG không phải hàm (là boolean true): không gọi được" {
		t.Errorf("env.DEBUG() = %s, want the not-a-function error", called.Text())
	}
}

// The env of an app is the app's own .env and nothing above it (docs/KITWORK_ENVIRONMENT.md §1,
// 22/10/2026). There used to be a fallback: an app whose .env was missing or empty read ../.env, and
// for apps/<identity>/ the parent is apps/, so one apps/.env reached every app without a .env of
// its own. The lookup now stops at the app's root.
func TestEnvCapabilityStopsAtAppRoot(t *testing.T) {
	apps := t.TempDir()
	if err := os.WriteFile(filepath.Join(apps, ".env"), []byte("LEAK=from-apps-folder\n"), 0644); err != nil {
		t.Fatal(err)
	}
	withoutEnv := filepath.Join(apps, "acme")
	withEmptyEnv := filepath.Join(apps, "globex")
	for _, dir := range []string{withoutEnv, withEmptyEnv} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(withEmptyEnv, ".env"), []byte("# nothing yet\n"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, root := range []string{withoutEnv, withEmptyEnv} {
		val, ok := capabilities.DefaultRegistry.Get("env", &mockScope{root: root})
		if !ok {
			t.Fatal("env capability not registered")
		}
		if got := val.Get("LEAK"); got.K != value.Nil {
			t.Errorf("app %s read apps/.env: LEAK = %q, want null", filepath.Base(root), got.Text())
		}
		if got := val.Invoke("require", value.NewString("LEAK")); got.K != value.Nil {
			t.Errorf("app %s: require(\"LEAK\") = %q, want null", filepath.Base(root), got.Text())
		}
	}
}
