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
}
