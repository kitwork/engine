package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb/managed"
)

func TestRootAuthOperatorCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "root")
	if _, err := managed.Init(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KITDB_AUTH_PASSWORD", "operator-admin-password")
	result, err := runRootAuth("auth-init", []string{path, "admin"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "operator-admin-password") || strings.Contains(string(encoded), "verifier") || strings.Contains(string(encoded), "salt") {
		t.Fatal("operator output leaked credential material")
	}
	t.Setenv("KITDB_AUTH_PASSWORD", "")
	if _, err := runRootAuth("auth-list", []string{path}); err != nil {
		t.Fatal(err)
	}
	if _, err := runRootAuth("auth-user", []string{"-role", "reader", "-databases", "missing", path, "reader"}); err == nil {
		t.Fatal("created invalid grant without password")
	}
	if _, err := runRootAuth("auth-user", []string{"-role", "admin", "-disabled", path, "admin"}); err == nil {
		t.Fatal("disabled last admin via CLI")
	}
	if _, err := runRootAuth("auth-init", []string{path, "admin"}); err == nil {
		t.Fatal("overwrote auth bootstrap")
	}
	if _, err := runRootAuth("auth-user", []string{"-role", "admin", "-permissions", "connect", path, "admin"}); err == nil {
		t.Fatal("mixed explicit role and permissions")
	}
}
