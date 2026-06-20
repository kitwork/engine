package work

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/value"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseDotEnv(t *testing.T) {
	p := writeEnv(t, "# comment\n\nPORT=9091\nexport FLAG=true\nQ=\"hi there\"\nS='abc'\n")
	vars := ParseDotEnv(p)
	want := map[string]string{"PORT": "9091", "FLAG": "true", "Q": "hi there", "S": "abc"}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
}

func TestParseDotEnv_Missing(t *testing.T) {
	vars := ParseDotEnv(filepath.Join(t.TempDir(), "nope.env"))
	if len(vars) != 0 {
		t.Fatalf("missing file should give empty map, got %v", vars)
	}
}

// env.KEY auto-coerces (number/bool/string) over the proxy's OWN map only.
func TestNewEnv_AccessorsAndIsolation(t *testing.T) {
	env := NewEnv(map[string]string{"PORT": "9091", "DEBUG": "true", "OFF": "false", "NAME": "kit"})
	if env.K != value.Proxy {
		t.Fatalf("expected Proxy, got %v", env.K)
	}

	// env.PORT → Number (auto-coerced from "9091")
	if got := env.Get("PORT"); got.K != value.Number || got.N != 9091 {
		t.Errorf("env.PORT = %v (K=%v), want number 9091", got, got.K)
	}
	// env.DEBUG → true, env.OFF → false (so `env.OFF || false` works correctly)
	if !env.Get("DEBUG").Truthy() {
		t.Errorf("env.DEBUG should coerce to true")
	}
	if env.Get("OFF").Truthy() {
		t.Errorf("env.OFF should coerce to false")
	}
	// env.NAME → String
	if got := env.Get("NAME").Text(); got != "kit" {
		t.Errorf("env.NAME = %q, want kit", got)
	}
	// ISOLATION: a key not in this map is invisible (another tenant's / host's)
	if got := env.Get("OTHER_TENANT_SECRET"); got.K != value.Nil {
		t.Errorf("foreign key must be nil, got %v", got)
	}
}

// env.require records misses for fail-fast.
func TestNewEnv_RequireMissing(t *testing.T) {
	var missing []string
	env := NewEnvWithMissing(map[string]string{"HAVE": "x"}, &missing)
	env.Invoke("require", value.NewString("HAVE"))
	env.Invoke("require", value.NewString("NEED_ME"))
	if len(missing) != 1 || missing[0] != "NEED_ME" {
		t.Fatalf("expected [NEED_ME] missing, got %v", missing)
	}
}
