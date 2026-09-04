package kitdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplicaBootstrapCatchUpAndRestart(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.kitdb")
	replicaPath := filepath.Join(directory, "replica.kitdb")
	source := mustOpenWithHistory(t, sourcePath)
	defer source.Close()

	commitPut(t, source, "product/1", "first")
	bootstrap, err := source.BootstrapReplica(context.Background(), "replica/eu", replicaPath)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	if bootstrap.Anchor.Path != replicaPath || bootstrap.Pin.Cursor != bootstrap.Anchor.Cursor() {
		t.Fatalf("bootstrap = %+v", bootstrap)
	}

	replica, err := OpenReplica(replicaPath, OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	if _, err := replica.Begin(); !errors.Is(err, ErrReplicaReadOnly) {
		t.Fatalf("replica Begin = %v, want ErrReplicaReadOnly", err)
	}
	stats, err := replica.Stats()
	if err != nil || !stats.ReplicaMode {
		t.Fatalf("replica Stats = (%+v, %v)", stats, err)
	}

	commitPut(t, source, "product/2", "second")
	catalogTx := mustBegin(t, source)
	if err := catalogTx.DefineStruct(testCatalogDefinition(productsCatalogID, "products", "replicated-hash")); err != nil {
		t.Fatalf("DefineStruct: %v", err)
	}
	if _, err := catalogTx.Commit(); err != nil {
		t.Fatalf("catalog Commit: %v", err)
	}
	commitPut(t, source, "product/1", "updated")
	tx := mustBegin(t, source)
	if err := tx.Delete([]byte("product/2")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatalf("delete Commit: %v", err)
	}

	catchUp, err := source.CatchUpReplica(context.Background(), "replica/eu", replica)
	if err != nil {
		t.Fatalf("CatchUpReplica: %v", err)
	}
	if catchUp.From != bootstrap.Anchor.Cursor() || catchUp.To != catchUp.SourceBoundary || catchUp.AppliedTransactions != 4 {
		t.Fatalf("catch-up = %+v", catchUp)
	}
	if got := string(requireValue(t, replica, "product/1")); got != "updated" {
		t.Fatalf("replica product/1 = %q", got)
	}
	if _, found, err := replica.Get([]byte("product/2")); err != nil || found {
		t.Fatalf("replica product/2 = (found %t, %v), want deleted", found, err)
	}
	if catchUp.Pin.Cursor != catchUp.To {
		t.Fatalf("catch-up pin = %+v, want %+v", catchUp.Pin.Cursor, catchUp.To)
	}
	catalog, err := replica.Catalog()
	if err != nil || len(catalog.Structs) != 1 || catalog.Structs[0].Hash != "replicated-hash" {
		t.Fatalf("replicated catalog = (%+v, %v)", catalog, err)
	}

	if err := replica.Close(); err != nil {
		t.Fatalf("close replica: %v", err)
	}
	replica, err = OpenReplica(replicaPath, OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatalf("reopen replica: %v", err)
	}
	defer replica.Close()
	resumed, err := source.CatchUpReplica(context.Background(), "replica/eu", replica)
	if err != nil {
		t.Fatalf("no-op CatchUpReplica: %v", err)
	}
	if resumed.AppliedTransactions != 0 || resumed.From != catchUp.To || resumed.To != catchUp.To {
		t.Fatalf("no-op catch-up = %+v", resumed)
	}
}

func TestReplicaCatchUpResumesAfterCancellation(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	replicaPath := filepath.Join(directory, "replica.kitdb")

	commitPut(t, source, "base", "one")
	bootstrap, err := source.BootstrapReplica(context.Background(), "replica/cancel", replicaPath)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	commitPut(t, source, "next/1", "two")
	commitPut(t, source, "next/2", "three")

	replica, err := OpenReplica(replicaPath, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	remove, err := replica.AddCommitListener(func(event CommitEvent) {
		if event.Transaction == bootstrap.Anchor.Transaction+1 {
			cancel()
		}
	})
	if err != nil {
		t.Fatalf("AddCommitListener: %v", err)
	}
	partial, err := source.CatchUpReplica(ctx, "replica/cancel", replica)
	remove()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("partial CatchUpReplica = (%+v, %v), want cancellation", partial, err)
	}
	if partial.AppliedTransactions != 1 || partial.To.Transaction != bootstrap.Anchor.Transaction+1 {
		t.Fatalf("partial catch-up = %+v", partial)
	}
	if err := replica.Close(); err != nil {
		t.Fatalf("close partial replica: %v", err)
	}

	replica, err = OpenReplica(replicaPath, OpenOptions{})
	if err != nil {
		t.Fatalf("reopen partial replica: %v", err)
	}
	defer replica.Close()
	resumed, err := source.CatchUpReplica(context.Background(), "replica/cancel", replica)
	if err != nil {
		t.Fatalf("resume CatchUpReplica: %v", err)
	}
	if resumed.From != partial.To || resumed.AppliedTransactions != 1 {
		t.Fatalf("resumed catch-up = %+v", resumed)
	}
	if got := string(requireValue(t, replica, "next/2")); got != "three" {
		t.Fatalf("resumed next/2 = %q", got)
	}
}

func TestReplicaRejectsDivergedTarget(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	replicaPath := filepath.Join(directory, "replica.kitdb")

	commitPut(t, source, "base", "source")
	if _, err := source.BootstrapReplica(context.Background(), "replica/diverged", replicaPath); err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	ordinary := mustOpen(t, replicaPath)
	commitPut(t, ordinary, "rogue", "target")
	if err := ordinary.Close(); err != nil {
		t.Fatalf("close diverged target: %v", err)
	}
	commitPut(t, source, "source-only", "value")
	if _, err := source.Checkpoint(); err != nil {
		t.Fatalf("source Checkpoint: %v", err)
	}

	replica, err := OpenReplica(replicaPath, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()
	before, err := replica.CurrentCursor()
	if err != nil {
		t.Fatalf("replica CurrentCursor: %v", err)
	}
	if _, err := source.CatchUpReplica(context.Background(), "replica/diverged", replica); !errors.Is(err, ErrHistoryCursor) {
		t.Fatalf("diverged CatchUpReplica = %v, want ErrHistoryCursor", err)
	}
	after, err := replica.CurrentCursor()
	if err != nil || after != before {
		t.Fatalf("diverged replica cursor changed from %+v to %+v (%v)", before, after, err)
	}
}

func TestReplicaRejectsDifferentDatabaseIdentity(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "source", "one")
	if _, err := source.Checkpoint(); err != nil {
		t.Fatalf("source Checkpoint: %v", err)
	}

	replica, err := OpenReplica(filepath.Join(directory, "unrelated.kitdb"), OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()
	if _, err := source.CatchUpReplica(context.Background(), "replica/unrelated", replica); !errors.Is(err, ErrReplicaDiverged) {
		t.Fatalf("identity CatchUpReplica = %v, want ErrReplicaDiverged", err)
	}
	pins, err := source.HistoryPins()
	if err != nil {
		t.Fatalf("HistoryPins: %v", err)
	}
	for _, pin := range pins {
		if pin.Name == "replica/unrelated" {
			t.Fatalf("identity failure created pin %+v", pin)
		}
	}
}

func TestReplicaCatchUpRequiresReplicaMode(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	replicaPath := filepath.Join(directory, "replica.kitdb")
	commitPut(t, source, "base", "one")
	if _, err := source.BootstrapReplica(context.Background(), "replica/mode", replicaPath); err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}

	ordinary := mustOpen(t, replicaPath)
	defer ordinary.Close()
	if _, err := source.CatchUpReplica(context.Background(), "replica/mode", ordinary); !errors.Is(err, ErrReplicaModeRequired) {
		t.Fatalf("ordinary CatchUpReplica = %v, want ErrReplicaModeRequired", err)
	}
}

func TestReplicaRejectsPrunedGap(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	replicaPath := filepath.Join(directory, "replica.kitdb")

	commitPut(t, source, "base", "one")
	if _, err := source.BootstrapReplica(context.Background(), "replica/gap", replicaPath); err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	if err := source.ReleaseHistoryPin(context.Background(), "replica/gap"); err != nil {
		t.Fatalf("ReleaseHistoryPin: %v", err)
	}
	commitPut(t, source, "later", "two")
	if _, err := source.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if _, err := source.PruneHistory(context.Background(), 2); err != nil {
		t.Fatalf("PruneHistory: %v", err)
	}

	replica, err := OpenReplica(replicaPath, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()
	if _, err := source.CatchUpReplica(context.Background(), "replica/gap", replica); !errors.Is(err, ErrHistoryGap) {
		t.Fatalf("gap CatchUpReplica = %v, want ErrHistoryGap", err)
	}
}

func TestReplicaRejectsInvalidEventBeforeCommit(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	replicaPath := filepath.Join(directory, "replica.kitdb")

	commitPut(t, source, "base", "one")
	bootstrap, err := source.BootstrapReplica(context.Background(), "replica/checksum", replicaPath)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	replica, err := OpenReplica(replicaPath, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()

	before, err := replica.CurrentCursor()
	if err != nil {
		t.Fatalf("CurrentCursor: %v", err)
	}
	_, err = replica.applyReplicaEvent(context.Background(), bootstrap.Anchor.DatabaseID, CommitEvent{
		Transaction: before.Transaction + 1,
		Checksum:    1,
		Operations: []CommitOperation{{
			Kind: CommitOperationPut, Key: []byte("bad"), Value: []byte("event"),
		}},
	})
	if !errors.Is(err, ErrReplicaDiverged) {
		t.Fatalf("applyReplicaEvent = %v, want ErrReplicaDiverged", err)
	}
	after, err := replica.CurrentCursor()
	if err != nil || after != before {
		t.Fatalf("invalid event changed cursor from %+v to %+v (%v)", before, after, err)
	}
	if _, found, err := replica.Get([]byte("bad")); err != nil || found {
		t.Fatalf("invalid event wrote data: found %t, err %v", found, err)
	}
}

func TestReplicaApplyIsIdempotentAtCurrentBoundary(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	replicaPath := filepath.Join(directory, "replica.kitdb")
	commitPut(t, source, "base", "one")
	bootstrap, err := source.BootstrapReplica(context.Background(), "replica/idempotent", replicaPath)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	commitPut(t, source, "next", "two")
	_, events := collectHistoryRange(t, source, bootstrap.Anchor.Transaction)
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one", events)
	}

	replica, err := OpenReplica(replicaPath, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()
	first, err := replica.applyReplicaEvent(context.Background(), source.ID(), events[0])
	if err != nil {
		t.Fatalf("first applyReplicaEvent: %v", err)
	}
	second, err := replica.applyReplicaEvent(context.Background(), source.ID(), events[0])
	if err != nil || second != first {
		t.Fatalf("second applyReplicaEvent = (%+v, %v), want %+v", second, err, first)
	}
	if got := string(requireValue(t, replica, "next")); got != "two" {
		t.Fatalf("idempotent value = %q", got)
	}
}

func TestReplicaBootstrapReleasesPinBeforePublicationFailure(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	destination := filepath.Join(directory, "exists.kitdb")
	if err := os.WriteFile(destination, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("write occupied destination: %v", err)
	}

	if _, err := source.BootstrapReplica(context.Background(), "replica/fail", destination); !errors.Is(err, ErrBackupExists) {
		t.Fatalf("BootstrapReplica = %v, want ErrBackupExists", err)
	}
	pins, err := source.HistoryPins()
	if err != nil {
		t.Fatalf("HistoryPins: %v", err)
	}
	for _, pin := range pins {
		if pin.Name == "replica/fail" {
			t.Fatalf("failed bootstrap leaked pin %+v", pin)
		}
	}
}

func TestReplicaBootstrapRefusesExistingName(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	firstPath := filepath.Join(directory, "first.kitdb")
	secondPath := filepath.Join(directory, "second.kitdb")
	if _, err := source.BootstrapReplica(context.Background(), "replica/stable", firstPath); err != nil {
		t.Fatalf("first BootstrapReplica: %v", err)
	}
	if _, err := source.BootstrapReplica(context.Background(), "replica/stable", secondPath); !errors.Is(err, ErrReplicaPinExists) {
		t.Fatalf("second BootstrapReplica = %v, want ErrReplicaPinExists", err)
	}
	if _, err := os.Stat(secondPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second replica path stat = %v, want not exist", err)
	}
}
