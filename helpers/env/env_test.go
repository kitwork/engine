package env_test

import (
	"os"
	"path/filepath"
	"testing"

	envhelper "github.com/kitwork/engine/helpers/env"
)

func TestParseDotEnv(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "env_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	envFile := filepath.Join(tmpDir, ".env")
	content := []byte(`# comment
PORT=8080
export HOST="localhost"
ALLOW_LOCAL=true
PI=3.14
`)
	if err := os.WriteFile(envFile, content, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	vars := envhelper.ParseDotEnv(envFile)
	if vars["PORT"] != "8080" {
		t.Errorf("Expected PORT=8080, got %q", vars["PORT"])
	}
	if vars["HOST"] != "localhost" {
		t.Errorf("Expected HOST=localhost, got %q", vars["HOST"])
	}
	if vars["ALLOW_LOCAL"] != "true" {
		t.Errorf("Expected ALLOW_LOCAL=true, got %q", vars["ALLOW_LOCAL"])
	}
}

func TestCoerce(t *testing.T) {
	if b, ok := envhelper.Coerce("true").(bool); !ok || !b {
		t.Errorf("Coerce 'true' expected true bool, got %v", b)
	}
	if n, ok := envhelper.Coerce("8080").(int); !ok || n != 8080 {
		t.Errorf("Coerce '8080' expected 8080 int, got %v", n)
	}
	if f, ok := envhelper.Coerce("3.14").(float64); !ok || f != 3.14 {
		t.Errorf("Coerce '3.14' expected 3.14 float64, got %v", f)
	}
	if s, ok := envhelper.Coerce("hello").(string); !ok || s != "hello" {
		t.Errorf("Coerce 'hello' expected 'hello' string, got %v", s)
	}
}
