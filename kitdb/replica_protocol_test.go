package kitdb

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestReplicaProtocolBatchesApplyAcknowledgeAndContinue(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "base", "anchor")
	bootstrap, err := source.BootstrapReplica(
		context.Background(),
		"replica/protocol",
		filepath.Join(directory, "replica.kitdb"),
	)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	for index := 1; index <= 5; index++ {
		commitPut(t, source, fmt.Sprintf("product/%d", index), fmt.Sprintf("value-%d", index))
	}

	replica, err := OpenReplica(bootstrap.Anchor.Path, OpenOptions{})
	if err != nil {
		t.Fatalf("OpenReplica: %v", err)
	}
	defer replica.Close()

	request := ReplicaBatchRequest{
		From: bootstrap.Anchor.Cursor(),
		Limits: ReplicaBatchLimits{
			MaxTransactions: 2,
			MaxBytes:        MaxReplicaBatchBytes,
		},
	}
	first, err := source.ReadReplicaBatch(context.Background(), request)
	if err != nil {
		t.Fatalf("first ReadReplicaBatch: %v", err)
	}
	if first.Version != ReplicaProtocolVersion || first.DatabaseID != source.ID() ||
		first.From != bootstrap.Anchor.Cursor() || len(first.Transactions) != 2 ||
		first.To.Transaction != bootstrap.Anchor.Transaction+2 || first.Complete() ||
		first.Bytes == 0 {
		t.Fatalf("first batch = %+v", first)
	}
	firstApply, err := replica.ApplyReplicaBatch(context.Background(), first)
	if err != nil || firstApply.AppliedTransactions != 2 || firstApply.To != first.To {
		t.Fatalf("first ApplyReplicaBatch = (%+v, %v)", firstApply, err)
	}
	duplicate, err := replica.ApplyReplicaBatch(context.Background(), first)
	if err != nil || duplicate.AppliedTransactions != 0 || duplicate.From != first.To || duplicate.To != first.To {
		t.Fatalf("duplicate ApplyReplicaBatch = (%+v, %v)", duplicate, err)
	}
	firstPin, err := source.AcknowledgeReplicaBatch(
		context.Background(), "replica/protocol", firstApply.Acknowledgement,
	)
	if err != nil || firstPin.Cursor != first.To {
		t.Fatalf("first AcknowledgeReplicaBatch = (%+v, %v)", firstPin, err)
	}
	lateTransaction := commitPut(t, source, "product/late", "outside-fixed-boundary")
	if lateTransaction != first.SourceBoundary.Transaction+1 {
		t.Fatalf("late transaction = %d, boundary = %+v", lateTransaction, first.SourceBoundary)
	}

	request.From = first.To
	request.SourceBoundary = first.SourceBoundary
	second, err := source.ReadReplicaBatch(context.Background(), request)
	if err != nil {
		t.Fatalf("second ReadReplicaBatch: %v", err)
	}
	if second.From != first.To || second.SourceBoundary != first.SourceBoundary ||
		len(second.Transactions) != 2 || second.Complete() {
		t.Fatalf("second batch = %+v", second)
	}
	secondApply, err := replica.ApplyReplicaBatch(context.Background(), second)
	if err != nil || secondApply.AppliedTransactions != 2 || secondApply.To != second.To {
		t.Fatalf("second ApplyReplicaBatch = (%+v, %v)", secondApply, err)
	}
	if _, err := source.AcknowledgeReplicaBatch(
		context.Background(), "replica/protocol", secondApply.Acknowledgement,
	); err != nil {
		t.Fatalf("second AcknowledgeReplicaBatch: %v", err)
	}

	request.From = second.To
	third, err := source.ReadReplicaBatch(context.Background(), request)
	if err != nil {
		t.Fatalf("third ReadReplicaBatch: %v", err)
	}
	if len(third.Transactions) != 1 || !third.Complete() || third.To != first.SourceBoundary {
		t.Fatalf("third batch = %+v", third)
	}
	thirdApply, err := replica.ApplyReplicaBatch(context.Background(), third)
	if err != nil || thirdApply.AppliedTransactions != 1 || thirdApply.To != third.To {
		t.Fatalf("third ApplyReplicaBatch = (%+v, %v)", thirdApply, err)
	}
	finalPin, err := source.AcknowledgeReplicaBatch(
		context.Background(), "replica/protocol", thirdApply.Acknowledgement,
	)
	if err != nil || finalPin.Cursor != third.SourceBoundary {
		t.Fatalf("final AcknowledgeReplicaBatch = (%+v, %v)", finalPin, err)
	}
	if got := string(requireValue(t, replica, "product/5")); got != "value-5" {
		t.Fatalf("replica product/5 = %q", got)
	}
	if _, found, err := replica.Get([]byte("product/late")); err != nil || found {
		t.Fatalf("fixed-boundary replica included late commit: found %t, err %v", found, err)
	}
}

func TestReplicaProtocolEnforcesTransactionAndByteLimits(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "base", "anchor")
	bootstrap, err := source.BootstrapReplica(
		context.Background(),
		"replica/limits",
		filepath.Join(directory, "replica.kitdb"),
	)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	commitPut(t, source, "first", "one")
	commitPut(t, source, "second", "two")

	complete, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From: bootstrap.Anchor.Cursor(),
	})
	if err != nil || len(complete.Transactions) != 2 || !complete.Complete() {
		t.Fatalf("complete ReadReplicaBatch = (%+v, %v)", complete, err)
	}
	firstBytes, err := replicaEventFrameBytes(complete.Transactions[0])
	if err != nil {
		t.Fatalf("replicaEventFrameBytes: %v", err)
	}
	bounded, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From:           bootstrap.Anchor.Cursor(),
		SourceBoundary: complete.SourceBoundary,
		Limits: ReplicaBatchLimits{
			MaxTransactions: 2,
			MaxBytes:        firstBytes,
		},
	})
	if err != nil || len(bounded.Transactions) != 1 || bounded.Bytes != firstBytes || bounded.Complete() {
		t.Fatalf("byte-bounded ReadReplicaBatch = (%+v, %v)", bounded, err)
	}
	tooSmall, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From:           bootstrap.Anchor.Cursor(),
		SourceBoundary: complete.SourceBoundary,
		Limits: ReplicaBatchLimits{
			MaxTransactions: 1,
			MaxBytes:        firstBytes - 1,
		},
	})
	if !errors.Is(err, ErrReplicaBatchLimit) || len(tooSmall.Transactions) != 0 {
		t.Fatalf("too-small ReadReplicaBatch = (%+v, %v)", tooSmall, err)
	}
	if _, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From: bootstrap.Anchor.Cursor(),
		Limits: ReplicaBatchLimits{
			MaxTransactions: MaxReplicaBatchTransactions + 1,
		},
	}); !errors.Is(err, ErrReplicaBatchLimit) {
		t.Fatalf("oversized transaction limit error = %v", err)
	}
	if _, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From: bootstrap.Anchor.Cursor(),
		Limits: ReplicaBatchLimits{
			MaxBytes: MaxReplicaBatchBytes + 1,
		},
	}); !errors.Is(err, ErrReplicaBatchLimit) {
		t.Fatalf("oversized byte limit error = %v", err)
	}
}

func TestReplicaProtocolRetriesPartialBatchAndRejectsCorruption(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "base", "anchor")
	partialBootstrap, err := source.BootstrapReplica(
		context.Background(),
		"replica/partial",
		filepath.Join(directory, "partial.kitdb"),
	)
	if err != nil {
		t.Fatalf("partial BootstrapReplica: %v", err)
	}
	corruptBootstrap, err := source.BootstrapReplica(
		context.Background(),
		"replica/corrupt",
		filepath.Join(directory, "corrupt.kitdb"),
	)
	if err != nil {
		t.Fatalf("corrupt BootstrapReplica: %v", err)
	}
	for index := 1; index <= 3; index++ {
		commitPut(t, source, fmt.Sprintf("next/%d", index), fmt.Sprintf("value-%d", index))
	}
	batch, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From: partialBootstrap.Anchor.Cursor(),
	})
	if err != nil || len(batch.Transactions) != 3 || !batch.Complete() {
		t.Fatalf("ReadReplicaBatch = (%+v, %v)", batch, err)
	}

	partial, err := OpenReplica(partialBootstrap.Anchor.Path, OpenOptions{})
	if err != nil {
		t.Fatalf("open partial replica: %v", err)
	}
	defer partial.Close()
	ctx, cancel := context.WithCancel(context.Background())
	remove, err := partial.AddCommitListener(func(event CommitEvent) {
		if event.Transaction == batch.Transactions[0].Transaction {
			cancel()
		}
	})
	if err != nil {
		t.Fatalf("AddCommitListener: %v", err)
	}
	firstApply, err := partial.ApplyReplicaBatch(ctx, batch)
	remove()
	if !errors.Is(err, context.Canceled) || firstApply.AppliedTransactions != 1 ||
		firstApply.To.Transaction != batch.Transactions[0].Transaction {
		t.Fatalf("partial ApplyReplicaBatch = (%+v, %v)", firstApply, err)
	}
	resumed, err := partial.ApplyReplicaBatch(context.Background(), batch)
	if err != nil || resumed.AppliedTransactions != 2 || resumed.From != firstApply.To || resumed.To != batch.To {
		t.Fatalf("resumed ApplyReplicaBatch = (%+v, %v)", resumed, err)
	}
	duplicate, err := partial.ApplyReplicaBatch(context.Background(), batch)
	if err != nil || duplicate.AppliedTransactions != 0 || duplicate.To != batch.To {
		t.Fatalf("duplicate ApplyReplicaBatch = (%+v, %v)", duplicate, err)
	}

	corrupt, err := OpenReplica(corruptBootstrap.Anchor.Path, OpenOptions{})
	if err != nil {
		t.Fatalf("open corrupt replica: %v", err)
	}
	defer corrupt.Close()
	before, err := corrupt.CurrentCursor()
	if err != nil {
		t.Fatalf("corrupt CurrentCursor: %v", err)
	}
	outOfOrder := cloneReplicaBatchForTest(batch)
	firstEventBytes, err := replicaEventFrameBytes(outOfOrder.Transactions[0])
	if err != nil {
		t.Fatalf("first replicaEventFrameBytes: %v", err)
	}
	outOfOrder.From = HistoryCursor{
		DatabaseID:  outOfOrder.DatabaseID,
		Transaction: outOfOrder.Transactions[0].Transaction,
		Checksum:    outOfOrder.Transactions[0].Checksum,
	}
	outOfOrder.Transactions = outOfOrder.Transactions[1:]
	outOfOrder.Bytes -= firstEventBytes
	if _, err := corrupt.ApplyReplicaBatch(context.Background(), outOfOrder); !errors.Is(err, ErrReplicaDiverged) {
		t.Fatalf("out-of-order ApplyReplicaBatch error = %v", err)
	}
	unchanged, err := corrupt.CurrentCursor()
	if err != nil || unchanged != before {
		t.Fatalf("out-of-order batch changed cursor from %+v to %+v (%v)", before, unchanged, err)
	}
	tampered := cloneReplicaBatchForTest(batch)
	tampered.Transactions[0].Checksum ^= 1
	if _, err := corrupt.ApplyReplicaBatch(context.Background(), tampered); !errors.Is(err, ErrReplicaDiverged) {
		t.Fatalf("tampered ApplyReplicaBatch error = %v", err)
	}
	after, err := corrupt.CurrentCursor()
	if err != nil || after != before {
		t.Fatalf("tampered batch changed cursor from %+v to %+v (%v)", before, after, err)
	}
	unsupported := cloneReplicaBatchForTest(batch)
	unsupported.Version++
	if _, err := corrupt.ApplyReplicaBatch(context.Background(), unsupported); !errors.Is(err, ErrReplicaProtocol) {
		t.Fatalf("unsupported ApplyReplicaBatch error = %v", err)
	}
}

func TestReplicaProtocolRejectsBadCursorAndAcknowledgement(t *testing.T) {
	directory := t.TempDir()
	source := mustOpenWithHistory(t, filepath.Join(directory, "source.kitdb"))
	defer source.Close()
	commitPut(t, source, "base", "anchor")
	bootstrap, err := source.BootstrapReplica(
		context.Background(),
		"replica/ack",
		filepath.Join(directory, "replica.kitdb"),
	)
	if err != nil {
		t.Fatalf("BootstrapReplica: %v", err)
	}
	commitPut(t, source, "next", "value")
	badFrom := bootstrap.Anchor.Cursor()
	badFrom.Checksum ^= 1
	if _, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From: badFrom,
	}); !errors.Is(err, ErrHistoryCursor) {
		t.Fatalf("bad cursor ReadReplicaBatch error = %v", err)
	}
	batch, err := source.ReadReplicaBatch(context.Background(), ReplicaBatchRequest{
		From: bootstrap.Anchor.Cursor(),
	})
	if err != nil {
		t.Fatalf("ReadReplicaBatch: %v", err)
	}
	wrong := replicaAcknowledgement(batch)
	wrong.Cursor.Checksum ^= 1
	if _, err := source.AcknowledgeReplicaBatch(
		context.Background(), "replica/ack", wrong,
	); !errors.Is(err, ErrHistoryCursor) {
		t.Fatalf("wrong AcknowledgeReplicaBatch error = %v", err)
	}
	pins, err := source.HistoryPins()
	if err != nil {
		t.Fatalf("HistoryPins: %v", err)
	}
	for _, pin := range pins {
		if pin.Name == "replica/ack" && pin.Cursor != bootstrap.Anchor.Cursor() {
			t.Fatalf("failed acknowledgement moved pin to %+v", pin.Cursor)
		}
	}
}

func cloneReplicaBatchForTest(batch ReplicaBatch) ReplicaBatch {
	cloned := batch
	cloned.Transactions = make([]CommitEvent, len(batch.Transactions))
	for index, event := range batch.Transactions {
		cloned.Transactions[index] = cloneReplicaCommitEvent(event)
	}
	return cloned
}
