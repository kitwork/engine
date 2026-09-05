package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestDistributionRefusesStableAndUnsafeTargets(t *testing.T) {
	for _, version := range []string{"v1.0.0", "v1.0.0-rc.0", "../../outside", "v1.0.0-rc.1 -X bad"} {
		if validate(version, []string{"linux/amd64"}) == nil {
			t.Fatalf("accepted %q", version)
		}
	}
	for _, targets := range [][]string{nil, {"darwin/arm64"}, {"linux/amd64", "linux/amd64"}} {
		if validate("v1.0.0-rc.1", targets) == nil {
			t.Fatalf("accepted targets %v", targets)
		}
	}
	if err := validate("v1.0.0-rc.1", []string{"windows/amd64", "linux/amd64"}); err != nil {
		t.Fatal(err)
	}
}

func TestDistributionRejectsKitworkRuntimeDependencies(t *testing.T) {
	for _, name := range []string{"work", "runtime", "compiler", "core", "capabilities/database", "kitdb-extra", "search-extra"} {
		if allowedPackage(modulePath + "/" + name) {
			t.Fatalf("accepted %s", name)
		}
	}
	if allowedPackage("github.com/lib/pq") || allowedPackage(modulePath+"/cmd/kitdbimport") {
		t.Fatal("RC admitted the importer before standalone COPY/KIMP support")
	}
	for _, name := range []string{modulePath + "/kitdb/relational", modulePath + "/search", modulePath + "/internal/snapshotfile"} {
		if !allowedPackage(name) {
			t.Fatalf("rejected %s", name)
		}
	}
}

func TestDistributionEnvironmentOverridesHostBuildFlags(t *testing.T) {
	got := buildEnvironment([]string{"GoFlags=-tags=unexpected", "CGO_ENABLED=1", "KEEP=ok"}, map[string]string{"GOFLAGS": "", "CGO_ENABLED": "0"})
	text := strings.Join(got, "\n")
	if len(got) != 3 || strings.Contains(text, "unexpected") || !strings.Contains(text, "CGO_ENABLED=0") || !strings.Contains(text, "KEEP=ok") {
		t.Fatal(text)
	}
}

func TestArchiveHashesPayloadAndRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bundle")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("independent KitDB binary fixture")
	if err := os.WriteFile(filepath.Join(dir, "kitdb"), payload, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "first.zip")
	second := filepath.Join(root, "second.zip")
	if err := archiveDirectory(dir, first); err != nil {
		t.Fatal(err)
	}
	if err := archiveDirectory(dir, second); err != nil {
		t.Fatal(err)
	}
	a, err := digestFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := digestFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 != b.SHA256 || a.Bytes != b.Bytes {
		t.Fatal("archive is not deterministic")
	}
	if err := archiveDirectory(dir, first); err == nil {
		t.Fatal("overwrote existing bundle")
	}
	reader, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if len(reader.File) != 1 || reader.File[0].Name != "kitdb" {
		t.Fatal("unexpected archive entries")
	}
	if reader.File[0].Mode().Perm() != 0o755 {
		t.Fatal("archive lost Unix executable permissions")
	}
	input, err := reader.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var restored bytes.Buffer
	if _, err := restored.ReadFrom(input); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, restored.Bytes()) {
		t.Fatal("archive payload changed")
	}
}

// This opt-in journey executes distribution binaries, not package test helpers.
func TestKitDBDistributionNativeJourney(t *testing.T) {
	dir := os.Getenv("KITDB_DIST_DIRECTORY")
	if dir == "" {
		t.Skip("set KITDB_DIST_DIRECTORY to an unpacked native RC bundle")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report manifest
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Format != "kitdb-distribution/v1" || report.Target != runtime.GOOS+"/"+runtime.GOARCH || report.CGOEnabled {
		t.Fatalf("invalid native manifest: %+v", report)
	}
	for _, file := range report.Files {
		if filepath.Base(file.Name) != file.Name {
			t.Fatal("non-local manifest entry")
		}
		actual, err := digestFile(filepath.Join(dir, file.Name))
		if err != nil {
			t.Fatal(err)
		}
		if actual != file {
			t.Fatalf("checksum mismatch: %s", file.Name)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	binary := func(name string) string {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join(dir, name)
	}
	for _, command := range commands {
		arg := "--version"
		if command == "kitdb" {
			arg = "version"
		}
		data, err := run(ctx, root, nil, binary(command), arg)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyVersion(data, command, report.Version, report.Commit); err != nil {
			t.Fatal(err)
		}
	}
	database := filepath.Join(root, "products.kitdb")
	query := func(statement string) json.RawMessage {
		t.Helper()
		data, err := run(ctx, root, nil, binary("kitdb"), "query", "--create", database, statement)
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Result json.RawMessage }
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		return response.Result
	}
	query("CREATE TABLE products (id BIGINT PRIMARY KEY, name TEXT NOT NULL SEARCHABLE, price BIGINT NOT NULL)")
	query("INSERT INTO products (id, name, price) VALUES (1, 'keyboard logitech', 100), (2, 'mouse', 50)")
	// NUMERIC aggregates use exact decimal strings; do not round through float64.
	var aggregate struct{ Rows [][]json.Number }
	if err := json.Unmarshal(query("SELECT COUNT(*), SUM(price) FROM products"), &aggregate); err != nil {
		t.Fatal(err)
	}
	if len(aggregate.Rows) != 1 || len(aggregate.Rows[0]) != 2 || aggregate.Rows[0][0] != "2" || aggregate.Rows[0][1] != "150" {
		t.Fatalf("aggregate: %+v", aggregate)
	}
	var hits struct{ Rows [][]json.Number }
	if err := json.Unmarshal(query("SELECT id FROM products WHERE * SEARCH 'keyboard logitech' LIMIT 10"), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits.Rows) != 1 || len(hits.Rows[0]) != 1 || hits.Rows[0][0] != "1" {
		t.Fatalf("search: %+v", hits)
	}
	exercisePostgresBinary(t, ctx, root, database, binary("kitdbpg"))
	data, err = run(ctx, root, nil, binary("kitdb"), "doctor", database)
	if err != nil {
		t.Fatal(err)
	}
	var doctor struct {
		Result struct {
			ControlledReady bool `json:"controlled_ready"`
			StableReady     bool `json:"stable_ready"`
		}
	}
	if err := json.Unmarshal(data, &doctor); err != nil {
		t.Fatal(err)
	}
	if !doctor.Result.ControlledReady || doctor.Result.StableReady {
		t.Fatalf("doctor: %+v", doctor)
	}
	if _, err := run(ctx, root, nil, binary("kitdbcanary"), "--duration=1s", "--tenants=4", "--workers=2", "--max-open=2", "--json=canary.json", "--quiet"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(root, "canary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var canary struct {
		Success    bool
		Build      struct{ Commit string }
		Completed  bool  `json:"workload_completed"`
		WorkloadMS int64 `json:"workload_ms"`
	}
	if err := json.Unmarshal(data, &canary); err != nil {
		t.Fatal(err)
	}
	if !canary.Success || !canary.Completed || canary.WorkloadMS < 1000 || canary.Build.Commit != report.Commit {
		t.Fatalf("canary: %+v", canary)
	}
	hash := sha256.Sum256(data)
	t.Logf("native bundle %s commit=%s passed SQL/search/pgwire/doctor/canary, canary sha256=%s", report.Version, report.Commit, hex.EncodeToString(hash[:]))
}

func exercisePostgresBinary(t *testing.T, ctx context.Context, root, database, server string) {
	t.Helper()
	command := exec.CommandContext(ctx, server, "-file", database, "-database", "products", "-listen", "127.0.0.1:0")
	command.Dir = root
	command.Env = buildEnvironment(os.Environ(), map[string]string{"KITDB_TOKEN": "distribution-fixture"})
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
	}()
	var address string
	select {
	case line := <-ready:
		const prefix = "KitDB standalone PostgreSQL profile listening on "
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("server did not report readiness: %q", line)
		}
		address = strings.TrimPrefix(line, prefix)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	endpoint := (&url.URL{Scheme: "postgres", User: url.UserPassword("kitdb", "distribution-fixture"), Host: address, Path: "/products", RawQuery: "sslmode=disable&connect_timeout=5"}).String()
	client, err := sql.Open("postgres", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetMaxOpenConns(1)
	transaction, err := client.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.ExecContext(ctx, "INSERT INTO products (id,name,price) VALUES (9,'rollback',900)"); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := client.QueryRowContext(ctx, "SELECT COUNT(*) FROM products").Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback count=%d err=%v", count, err)
	}
	// Standalone COPY is deliberately not part of this RC profile. A rejected
	// operation must leave the connection usable and the committed rows intact.
	if _, err := client.ExecContext(ctx, "COPY products FROM STDIN"); err == nil {
		t.Fatal("standalone COPY unexpectedly accepted without qualified support")
	}
	var total int64
	if err := client.QueryRowContext(ctx, "SELECT SUM(price) FROM products").Scan(&total); err != nil || total != 150 {
		t.Fatalf("post-rejection total=%d err=%v", total, err)
	}
}
