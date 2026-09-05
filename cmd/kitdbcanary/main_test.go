package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKitDBCanaryCompletesBoundedJourney(t *testing.T) {
	config := canaryConfig{
		Duration:        250 * time.Millisecond,
		Tenants:         4,
		Workers:         4,
		MaxOpen:         2,
		Keyspace:        32,
		CheckpointEvery: 8,
		VerifyEvery:     32,
		HistoryBytes:    1 << 20,
		Seed:            20260828,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := executeCanary(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Contract.Contract != "kitdb/1" ||
		!report.WorkloadCompleted || report.WorkloadMS < config.Duration.Milliseconds() ||
		report.RequestedWorkloadMS != config.Duration.Milliseconds() || report.Build.Version == "" ||
		report.Commits < uint64(config.Tenants) ||
		report.Backups != uint64(config.Tenants) ||
		report.Restores != uint64(config.Tenants) ||
		report.Node.ActiveLeases != 0 || !report.Node.Closed {
		t.Fatalf("canary report = %#v", report)
	}

	path := filepath.Join(t.TempDir(), "report.json")
	if err := writeCanaryReport(path, report); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded canaryReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Success || decoded.Commits != report.Commits || decoded.Contract != report.Contract {
		t.Fatalf("decoded report = %#v", decoded)
	}
}

func TestKitDBCanaryCancellationCannotQualifyFullWorkload(t *testing.T) {
	config := canaryConfig{Duration: time.Hour, Interval: time.Millisecond, Tenants: 2, Workers: 1, MaxOpen: 1,
		Keyspace: 16, CheckpointEvery: 8, VerifyEvery: 32, HistoryBytes: 1 << 20}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	report, err := executeCanary(ctx, config)
	if err == nil || report.Success || report.WorkloadCompleted || report.WorkloadMS >= config.Duration.Milliseconds() {
		t.Fatalf("canceled canary qualified: %+v err=%v", report, err)
	}
}

func TestKitDBCanaryRejectsUnboundedConfiguration(t *testing.T) {
	config := canaryConfig{
		Duration:        time.Second,
		Tenants:         2,
		Workers:         1,
		MaxOpen:         3,
		Keyspace:        1,
		CheckpointEvery: 1,
		VerifyEvery:     1,
		HistoryBytes:    1,
	}
	if err := config.validate(); err == nil {
		t.Fatal("max-open greater than tenants was accepted")
	}
	config.MaxOpen = 1
	config.Keyspace = 1_000_001
	if err := config.validate(); err == nil {
		t.Fatal("unbounded keyspace was accepted")
	}
}
