package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestDoctorProvesControlledProjectReadiness(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "project.kitdb")
	database, err := kitdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	putCommandValue(t, database, "project/1", "one")
	putCommandValue(t, database, "project/2", "two")
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	id := database.ID()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := runDoctor(context.Background(), []string{"--expected-id", id, path})
	if err != nil {
		t.Fatal(err)
	}
	if result.Format != doctorFormat || result.DatabaseID != id ||
		result.Transaction != 2 || result.Records != 2 ||
		result.LogicalSHA256 == "" || result.Backup.SHA256 == "" ||
		!result.ControlledReady || result.StableReady {
		t.Fatalf("doctor result = %#v", result)
	}
	if len(result.Checks) != 8 || result.Checks[7].Name != "stable_release" ||
		result.Checks[7].Passed {
		t.Fatalf("doctor checks = %#v", result.Checks)
	}

	response := runAndDecode(t, "doctor", "--expected-id", id, path)
	if response.Format != commandFormat || response.Command != "doctor" {
		t.Fatalf("doctor envelope = %#v", response)
	}
	var viaCLI doctorResult
	if err := json.Unmarshal(response.Result, &viaCLI); err != nil {
		t.Fatal(err)
	}
	if !viaCLI.ControlledReady || viaCLI.DatabaseID != id || viaCLI.LogicalSHA256 != result.LogicalSHA256 {
		t.Fatalf("doctor CLI result = %#v", viaCLI)
	}
	serialized := string(response.Result)
	if strings.Contains(serialized, path) || strings.Contains(serialized, "project/1") ||
		strings.Contains(serialized, "one") {
		t.Fatalf("doctor leaked tenant data: %s", serialized)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".kitdb-doctor-") {
			t.Fatalf("doctor workspace was not cleaned: %s", entry.Name())
		}
	}
}

func TestDoctorRejectsWrongIdentityAndUnsafeBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.kitdb")
	database, err := kitdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := runDoctor(context.Background(), []string{"--expected-id", "wrong", path}); err == nil {
		t.Fatal("doctor accepted the wrong database identity")
	}
	if _, err := runDoctor(context.Background(), []string{"--max-wal-bytes", "0", path}); err == nil {
		t.Fatal("doctor accepted an unsafe WAL bound")
	}
}

func TestDoctorRequiresOfflineWriterAndRejectsCorruption(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "project.kitdb")
	database, err := kitdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	putCommandValue(t, database, "project/1", "one")
	if _, err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if _, err := runDoctor(context.Background(), []string{path}); err == nil {
		t.Fatal("doctor admitted a database with an active writer")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, info.Size()-1); err != nil {
		t.Fatal(err)
	}
	if _, err := runDoctor(context.Background(), []string{path}); err == nil {
		t.Fatal("doctor admitted a truncated database")
	}
}
