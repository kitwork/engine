package work

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathUtilityCapability(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-path-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	script := `
import { path, log } from "kitwork";

// Test Join
const joined = path.Join("a", "b", "c");
log.Print("JOIN: " + joined);
if (joined != "a/b/c") fail("join failed");

// Test Clean
const cleaned = path.Clean("a/b/../c");
log.Print("CLEAN: " + cleaned);
if (cleaned != "a/c") fail("clean failed");

// Test Dirname
const dir = path.Dirname("a/b/c");
log.Print("DIR: " + dir);
if (dir != "a/b") fail("dirname failed");

// Test Basename
const base = path.Basename("a/b/c.txt");
const baseNoExt = path.Basename("a/b/c.txt", ".txt");
log.Print("BASE: " + base + " BASE_NO_EXT: " + baseNoExt);
if (base != "c.txt") fail("basename failed");
if (baseNoExt != "c") fail("basename strip ext failed");

// Test Extname
const ext = path.Extname("a/b/c.txt");
log.Print("EXT: " + ext);
if (ext != ".txt") fail("extname failed");

// Test Resolve
const resolved = path.Resolve("a", "b", "../c");
log.Print("RESOLVE: " + resolved);
if (resolved != "/a/c") fail("resolve failed");
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}

	if err := NewTenant(tmpDir, "localhost").Run(); err != nil {
		t.Fatalf("path utility E2E test failed at runtime: %v", err)
	}
}
