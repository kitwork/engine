package kitdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReplicaFileTransportPublishesAppliesAcknowledgesAndCleans(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "base", "anchor")
	bootstrap, err := source.BootstrapReplica(
		context.Background(), "replica/file-basic", filepath.Join(directory, "replica.kitdb"),
	)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	for index := 1; index <= 3; index++ {
		commitPut(t, source, fmt.Sprintf("product/%d", index), fmt.Sprintf("value-%d", index))
	}
	batch, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{From: bootstrap.Anchor.Cursor()})
	if err != nil {
		t.Fatalf("ReadReplicaBatch: %v", err)
	}
	replica, err := OpenReplica(bootstrap.Anchor.Path, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()
	transport, err := OpenReplicaFileTransport(filepath.Join(directory, "mailbox"), ReplicaFileTransportLimits{})
	if err != nil {
		t.Fatalf("OpenReplicaFileTransport: %v", err)
	}

	publication, err := transport.PublishBatch(context.Background(), batch)
	if err != nil || publication.AlreadyPublished || publication.Path == "" || publication.Transactions != 3 {
		t.Fatalf("PublishBatch = (%+v, %v)", publication, err)
	}
	repeated, err := transport.PublishBatch(context.Background(), batch)
	if err != nil || !repeated.AlreadyPublished || repeated.Path != publication.Path {
		t.Fatalf("repeated PublishBatch = (%+v, %v)", repeated, err)
	}
	stats, err := transport.Stats(context.Background())
	if err != nil || stats.PendingBatches != 1 || stats.PendingAcknowledgements != 0 || stats.PendingBytes != publication.Bytes {
		t.Fatalf("stats after publish = (%+v, %v)", stats, err)
	}

	applied, found, err := transport.ApplyNext(context.Background(), replica)
	if err != nil || !found || applied.AppliedTransactions != 3 || applied.To != batch.To || applied.AcknowledgementPath == "" {
		t.Fatalf("ApplyNext = (%+v, %t, %v)", applied, found, err)
	}
	stats, err = transport.Stats(context.Background())
	if err != nil || stats.PendingBatches != 1 || stats.PendingAcknowledgements != 1 {
		t.Fatalf("stats after apply = (%+v, %v)", stats, err)
	}

	acknowledged, found, err := transport.AcknowledgeNext(context.Background(), source, "replica/file-basic")
	if err != nil || !found || acknowledged.Cursor != batch.To || acknowledged.Pin.Cursor != batch.To || acknowledged.AlreadyAcknowledged {
		t.Fatalf("AcknowledgeNext = (%+v, %t, %v)", acknowledged, found, err)
	}
	stats, err = transport.Stats(context.Background())
	if err != nil || stats != (ReplicaFileTransportStats{}) {
		t.Fatalf("stats after cleanup = (%+v, %v)", stats, err)
	}
	if _, found, err := transport.ApplyNext(context.Background(), replica); err != nil || found {
		t.Fatalf("empty ApplyNext = (%t, %v)", found, err)
	}
	if _, found, err := transport.AcknowledgeNext(context.Background(), source, "replica/file-basic"); err != nil || found {
		t.Fatalf("empty AcknowledgeNext = (%t, %v)", found, err)
	}
	if got := string(requireValue(t, replica, "product/3")); got != "value-3" {
		t.Fatalf("replica product/3 = %q", got)
	}
}

func TestReplicaFileTransportResumesPartialTargetAndAdvancedSourcePin(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "base", "anchor")
	bootstrap, err := source.BootstrapReplica(
		context.Background(), "replica/file-resume", filepath.Join(directory, "replica.kitdb"),
	)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	for index := 1; index <= 3; index++ {
		commitPut(t, source, fmt.Sprintf("next/%d", index), fmt.Sprintf("value-%d", index))
	}
	batch, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{From: bootstrap.Anchor.Cursor()})
	if err != nil {
		t.Fatalf("ReadReplicaBatch: %v", err)
	}
	transportPath := filepath.Join(directory, "mailbox")
	transport, err := OpenReplicaFileTransport(transportPath, ReplicaFileTransportLimits{})
	if err != nil {
		t.Fatalf("OpenReplicaFileTransport: %v", err)
	}
	if _, err := transport.PublishBatch(context.Background(), batch); err != nil {
		t.Fatalf("PublishBatch: %v", err)
	}
	replica, err := OpenReplica(bootstrap.Anchor.Path, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	remove, err := replica.AddCommitListener(func(event CommitEvent) {
		if event.Transaction == batch.Transactions[0].Transaction {
			cancel()
		}
	})
	if err != nil {
		t.Fatalf("AddCommitListener: %v", err)
	}
	partial, found, err := transport.ApplyNext(ctx, replica)
	remove()
	if !found || !errors.Is(err, context.Canceled) || partial.AppliedTransactions != 1 || partial.To.Transaction != batch.Transactions[0].Transaction {
		t.Fatalf("partial ApplyNext = (%+v, %t, %v)", partial, found, err)
	}
	stats, err := transport.Stats(context.Background())
	if err != nil || stats.PendingAcknowledgements != 0 || stats.PendingBatches != 1 {
		t.Fatalf("partial stats = (%+v, %v)", stats, err)
	}
	if err := replica.Close(); err != nil {
		t.Fatalf("close partial replica: %v", err)
	}
	replica, err = OpenReplica(bootstrap.Anchor.Path, OpenOptions{})
	if err != nil {
		t.Fatalf("reopen partial replica: %v", err)
	}
	defer replica.Close()
	transport, err = OpenReplicaFileTransport(transportPath, ReplicaFileTransportLimits{})
	if err != nil {
		t.Fatalf("reopen transport: %v", err)
	}
	resumed, found, err := transport.ApplyNext(context.Background(), replica)
	if err != nil || !found || resumed.AppliedTransactions != 2 || resumed.To != batch.To {
		t.Fatalf("resumed ApplyNext = (%+v, %t, %v)", resumed, found, err)
	}

	// Simulate a crash after the source pin publication but before either
	// transport file was removed.
	pin, err := source.AcknowledgeReplicaBatch(
		context.Background(), "replica/file-resume", replicaAcknowledgement(batch),
	)
	if err != nil || pin.Cursor != batch.To {
		t.Fatalf("manual AcknowledgeReplicaBatch = (%+v, %v)", pin, err)
	}
	cleanup, found, err := transport.AcknowledgeNext(context.Background(), source, "replica/file-resume")
	if err != nil || !found || !cleanup.AlreadyAcknowledged || cleanup.Pin.Cursor != batch.To {
		t.Fatalf("replayed AcknowledgeNext = (%+v, %t, %v)", cleanup, found, err)
	}
}

func TestReplicaFileTransportRegeneratesMissingAcknowledgement(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "base", "anchor")
	bootstrap, err := source.BootstrapReplica(
		context.Background(), "replica/file-missing-ack", filepath.Join(directory, "replica.kitdb"),
	)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	commitPut(t, source, "next", "value")
	batch, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{From: bootstrap.Anchor.Cursor()})
	if err != nil {
		t.Fatalf("ReadReplicaBatch: %v", err)
	}
	transport, err := OpenReplicaFileTransport(filepath.Join(directory, "mailbox"), ReplicaFileTransportLimits{})
	if err != nil {
		t.Fatalf("OpenReplicaFileTransport: %v", err)
	}
	if _, err := transport.PublishBatch(context.Background(), batch); err != nil {
		t.Fatalf("PublishBatch: %v", err)
	}
	replica, err := OpenReplica(bootstrap.Anchor.Path, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()
	if applied, err := replica.ApplyReplicaBatch(context.Background(), batch); err != nil || applied.To != batch.To {
		t.Fatalf("direct ApplyReplicaBatch = (%+v, %v)", applied, err)
	}

	replayed, found, err := transport.ApplyNext(context.Background(), replica)
	if err != nil || !found || replayed.AppliedTransactions != 0 || replayed.To != batch.To || replayed.AcknowledgementPath == "" {
		t.Fatalf("ACK regeneration ApplyNext = (%+v, %t, %v)", replayed, found, err)
	}
}

func TestReplicaFileTransportNeverMovesPinBackward(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "base", "anchor")
	bootstrap, err := source.BootstrapReplica(
		context.Background(), "replica/file-monotonic", filepath.Join(directory, "replica.kitdb"),
	)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	commitPut(t, source, "next/1", "one")
	commitPut(t, source, "next/2", "two")
	first, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From:   bootstrap.Anchor.Cursor(),
		Limits: ReplicaBatchLimits{MaxTransactions: 1, MaxBytes: MaxReplicaBatchBytes},
	})
	if err != nil {
		t.Fatalf("first ReadReplicaBatch: %v", err)
	}
	second, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From: first.To, SourceBoundary: first.SourceBoundary,
		Limits: ReplicaBatchLimits{MaxTransactions: 1, MaxBytes: MaxReplicaBatchBytes},
	})
	if err != nil || !second.Complete() {
		t.Fatalf("second ReadReplicaBatch = (%+v, %v)", second, err)
	}
	transport, err := OpenReplicaFileTransport(filepath.Join(directory, "mailbox"), ReplicaFileTransportLimits{})
	if err != nil {
		t.Fatalf("OpenReplicaFileTransport: %v", err)
	}
	if _, err := transport.PublishBatch(context.Background(), first); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	if _, err := transport.PublishBatch(context.Background(), second); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	replica, err := OpenReplica(bootstrap.Anchor.Path, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()
	for index := 0; index < 2; index++ {
		if _, found, err := transport.ApplyNext(context.Background(), replica); err != nil || !found {
			t.Fatalf("ApplyNext %d = (%t, %v)", index, found, err)
		}
	}
	if _, err := source.AcknowledgeReplicaBatch(
		context.Background(), "replica/file-monotonic", replicaAcknowledgement(second),
	); err != nil {
		t.Fatalf("advance source pin to second: %v", err)
	}
	old, found, err := transport.AcknowledgeNext(context.Background(), source, "replica/file-monotonic")
	if err != nil || !found || !old.AlreadyAcknowledged || old.Cursor != first.To || old.Pin.Cursor != second.To {
		t.Fatalf("old AcknowledgeNext = (%+v, %t, %v)", old, found, err)
	}
	latest, found, err := transport.AcknowledgeNext(context.Background(), source, "replica/file-monotonic")
	if err != nil || !found || !latest.AlreadyAcknowledged || latest.Cursor != second.To || latest.Pin.Cursor != second.To {
		t.Fatalf("latest AcknowledgeNext = (%+v, %t, %v)", latest, found, err)
	}
}

func TestReplicaFileTransportRejectsConflictCorruptionAndQuota(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "base", "anchor")
	bootstrap, err := source.BootstrapReplica(
		context.Background(), "replica/file-limits", filepath.Join(directory, "replica.kitdb"),
	)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	commitPut(t, source, "next/1", "one")
	commitPut(t, source, "next/2", "two")
	first, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From:   bootstrap.Anchor.Cursor(),
		Limits: ReplicaBatchLimits{MaxTransactions: 1, MaxBytes: MaxReplicaBatchBytes},
	})
	if err != nil {
		t.Fatalf("first ReadReplicaBatch: %v", err)
	}
	second, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From: first.To, SourceBoundary: first.SourceBoundary,
		Limits: ReplicaBatchLimits{MaxTransactions: 1, MaxBytes: MaxReplicaBatchBytes},
	})
	if err != nil {
		t.Fatalf("second ReadReplicaBatch: %v", err)
	}
	transport, err := OpenReplicaFileTransport(filepath.Join(directory, "mailbox"), ReplicaFileTransportLimits{
		MaxPendingBatches: 1,
		MaxPendingBytes:   1 << 20,
	})
	if err != nil {
		t.Fatalf("OpenReplicaFileTransport: %v", err)
	}
	publication, err := transport.PublishBatch(context.Background(), first)
	if err != nil {
		t.Fatalf("PublishBatch: %v", err)
	}
	original, err := os.ReadFile(publication.Path)
	if err != nil {
		t.Fatalf("read published batch: %v", err)
	}
	conflict := cloneReplicaBatchForTest(first)
	conflict.SourceBoundary.Checksum ^= 1
	if _, err := transport.PublishBatch(context.Background(), conflict); !errors.Is(err, ErrReplicaTransportConflict) {
		t.Fatalf("conflicting PublishBatch error = %v", err)
	}
	unchanged, err := os.ReadFile(publication.Path)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("conflicting publish replaced bytes: %v", err)
	}
	if _, err := transport.PublishBatch(context.Background(), second); !errors.Is(err, ErrReplicaTransportLimit) {
		t.Fatalf("quota PublishBatch error = %v", err)
	}

	corrupt := append([]byte(nil), original...)
	corrupt[len(corrupt)-1] ^= 1
	if err := os.WriteFile(publication.Path, corrupt, 0o600); err != nil {
		t.Fatalf("corrupt published batch: %v", err)
	}
	replica, err := OpenReplica(bootstrap.Anchor.Path, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()
	before, err := replica.CurrentCursor()
	if err != nil {
		t.Fatalf("CurrentCursor before corruption: %v", err)
	}
	if _, found, err := transport.ApplyNext(context.Background(), replica); !found || !errors.Is(err, ErrReplicaProtocol) {
		t.Fatalf("corrupt ApplyNext = (%t, %v)", found, err)
	}
	after, err := replica.CurrentCursor()
	if err != nil || after != before {
		t.Fatalf("corrupt batch changed target from %+v to %+v (%v)", before, after, err)
	}
}

func TestReplicaFileTransportIgnoresAndExplicitlyCleansStaging(t *testing.T) {
	directory := t.TempDir()
	transport, err := OpenReplicaFileTransport(filepath.Join(directory, "mailbox"), ReplicaFileTransportLimits{})
	if err != nil {
		t.Fatalf("OpenReplicaFileTransport: %v", err)
	}
	batchStaging := filepath.Join(transport.batchPath, replicaFileTemporaryMark+"batch-crash.tmp")
	ackStaging := filepath.Join(transport.ackPath, replicaFileTemporaryMark+"ack-crash.tmp")
	if err := os.WriteFile(batchStaging, []byte("partial-batch"), 0o600); err != nil {
		t.Fatalf("write batch staging: %v", err)
	}
	if err := os.WriteFile(ackStaging, []byte("partial-ack"), 0o600); err != nil {
		t.Fatalf("write ACK staging: %v", err)
	}
	stats, err := transport.Stats(context.Background())
	if err != nil || stats.StagingFiles != 2 || stats.PendingBatches != 0 || stats.PendingAcknowledgements != 0 {
		t.Fatalf("staging stats = (%+v, %v)", stats, err)
	}
	cleanup, err := transport.CleanupStaging(context.Background())
	if err != nil || cleanup.Files != 2 || cleanup.Bytes != uint64(len("partial-batch")+len("partial-ack")) {
		t.Fatalf("CleanupStaging = (%+v, %v)", cleanup, err)
	}
	stats, err = transport.Stats(context.Background())
	if err != nil || stats != (ReplicaFileTransportStats{}) {
		t.Fatalf("stats after staging cleanup = (%+v, %v)", stats, err)
	}
}
