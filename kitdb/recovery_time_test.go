package kitdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreToTimeSelectsCommittedPrefixAcrossRestart(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.kitdb")
	destinationPath := filepath.Join(directory, "recovered.kitdb")
	db := mustOpenWithHistory(t, sourcePath)

	events := make([]CommitEvent, 0, 3)
	remove, err := db.AddCommitListener(func(event CommitEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "product/1", "first")
	commitPut(t, db, "product/2", "second")
	commitPut(t, db, "product/1", "updated")
	remove()
	if len(events) != 3 {
		t.Fatalf("commit events = %d, want 3", len(events))
	}
	for index, event := range events {
		if event.CommittedAt.IsZero() {
			t.Fatalf("event %d has no commit timestamp", index)
		}
		if index != 0 && !event.CommittedAt.After(events[index-1].CommittedAt) {
			t.Fatalf("event timestamps are not increasing: %s then %s", events[index-1].CommittedAt, event.CommittedAt)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, sourcePath)
	defer db.Close()
	result, err := db.RestoreToTime(context.Background(), destinationPath, events[1].CommittedAt)
	if err != nil {
		t.Fatalf("RestoreToTime: %v", err)
	}
	if result.Transaction != events[1].Transaction || !result.ResolvedAt.Equal(events[1].CommittedAt) {
		t.Fatalf("time restore = transaction %d at %s, want %d at %s", result.Transaction, result.ResolvedAt, events[1].Transaction, events[1].CommittedAt)
	}

	recovered := mustOpen(t, destinationPath)
	defer recovered.Close()
	if got := string(requireValue(t, recovered, "product/1")); got != "first" {
		t.Fatalf("recovered product/1 = %q, want first", got)
	}
	if got := string(requireValue(t, recovered, "product/2")); got != "second" {
		t.Fatalf("recovered product/2 = %q, want second", got)
	}
}

func TestForkToTimeCreatesIndependentDatabaseIdentity(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.kitdb")
	db, err := OpenWithOptions(path, OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	var events []CommitEvent
	remove, err := db.AddCommitListener(func(event CommitEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "product/1", "before")
	commitPut(t, db, "product/1", "after")
	remove()

	destination := filepath.Join(directory, "fork.kitdb")
	result, err := db.ForkToTime(context.Background(), destination, events[0].CommittedAt)
	if err != nil {
		t.Fatalf("ForkToTime: %v", err)
	}
	if result.DatabaseID == db.ID() {
		t.Fatalf("fork identity = source identity %q", result.DatabaseID)
	}
	if result.Transaction != events[0].Transaction || !result.ResolvedAt.Equal(events[0].CommittedAt) {
		t.Fatalf("fork boundary = transaction %d at %s, want %d at %s", result.Transaction, result.ResolvedAt, events[0].Transaction, events[0].CommittedAt)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	fork, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()
	if value := string(requireValue(t, fork, "product/1")); value != "before" {
		t.Fatalf("fork product = %q, want before", value)
	}
	commitPut(t, fork, "product/2", "independent")
}

func TestRestoreToTimeRejectsTargetBeforeRecoveryWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.kitdb")
	db := mustOpenWithHistory(t, path)
	defer db.Close()

	metadata, err := readHistoryMetadata(filepath.Join(databaseHistoryPath(path), historyMetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	target := commitTimeValue(metadata.baseCommitTime).Add(-time.Nanosecond)
	_, err = db.RestoreToTime(context.Background(), filepath.Join(filepath.Dir(path), "too-old.kitdb"), target)
	if !errors.Is(err, ErrRecoveryTargetTooOld) {
		t.Fatalf("RestoreToTime before window = %v, want ErrRecoveryTargetTooOld", err)
	}
}

func TestEnablingTimeRecoveryRebasesExistingWALWithoutInventingHistory(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "existing.kitdb")
	db := mustOpen(t, path)
	var oldEvent CommitEvent
	remove, err := db.AddCommitListener(func(event CommitEvent) {
		oldEvent = event
	})
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "product/1", "before-recovery")
	remove()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpenWithHistory(t, path)
	defer db.Close()
	metadata, err := readHistoryMetadata(filepath.Join(databaseHistoryPath(path), historyMetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.baseTx != oldEvent.Transaction {
		t.Fatalf("recovery base transaction = %d, want %d", metadata.baseTx, oldEvent.Transaction)
	}
	baseTime := commitTimeValue(metadata.baseCommitTime)
	if baseTime.Before(oldEvent.CommittedAt) {
		t.Fatalf("recovery base time %s precedes recovered WAL time %s", baseTime, oldEvent.CommittedAt)
	}
	if _, err := db.RestoreToTime(
		context.Background(), filepath.Join(directory, "too-early.kitdb"), oldEvent.CommittedAt,
	); !errors.Is(err, ErrRecoveryTargetTooOld) {
		t.Fatalf("restore before newly enabled recovery = %v, want ErrRecoveryTargetTooOld", err)
	}

	var retainedEvent CommitEvent
	remove, err = db.AddCommitListener(func(event CommitEvent) {
		retainedEvent = event
	})
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "product/2", "retained")
	remove()
	destination := filepath.Join(directory, "retained.kitdb")
	if _, err := db.RestoreToTime(context.Background(), destination, retainedEvent.CommittedAt); err != nil {
		t.Fatalf("restore after enabling recovery: %v", err)
	}
	recovered := mustOpen(t, destination)
	defer recovered.Close()
	if got := string(requireValue(t, recovered, "product/1")); got != "before-recovery" {
		t.Fatalf("recovered baseline value = %q", got)
	}
	if got := string(requireValue(t, recovered, "product/2")); got != "retained" {
		t.Fatalf("recovered retained value = %q", got)
	}
}

func TestHistoryPruneAdvancesTimeRecoveryAnchor(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.kitdb")
	db := mustOpenWithHistory(t, path)
	defer db.Close()

	events := make([]CommitEvent, 0, 2)
	remove, err := db.AddCommitListener(func(event CommitEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "one", "first")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	commitPut(t, db, "two", "second")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	remove()

	pruned, err := db.PruneHistory(context.Background(), events[0].Transaction)
	if err != nil {
		t.Fatalf("PruneHistory: %v", err)
	}
	if pruned.BaseTransaction != events[0].Transaction {
		t.Fatalf("pruned base = %d, want %d", pruned.BaseTransaction, events[0].Transaction)
	}
	if _, err := VerifyBackupAnchor(context.Background(), historyBaseAnchorPath(databaseHistoryPath(path), events[0].Transaction)); err != nil {
		t.Fatalf("advanced history base anchor: %v", err)
	}

	destination := filepath.Join(directory, "after-prune.kitdb")
	result, err := db.RestoreToTime(context.Background(), destination, events[1].CommittedAt)
	if err != nil {
		t.Fatalf("RestoreToTime after prune: %v", err)
	}
	if result.Transaction != events[1].Transaction {
		t.Fatalf("restored transaction = %d, want %d", result.Transaction, events[1].Transaction)
	}
	recovered := mustOpen(t, destination)
	defer recovered.Close()
	if got := string(requireValue(t, recovered, "one")); got != "first" {
		t.Fatalf("recovered one = %q", got)
	}
	if got := string(requireValue(t, recovered, "two")); got != "second" {
		t.Fatalf("recovered two = %q", got)
	}
}
