package kitdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestLogicalDigestMatchesVerifiedBackupAndRestore(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.kitdb")
	database, err := Open(source)
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, database, "b", "two")
	commitPut(t, database, "a", "one")

	live, err := database.LogicalDigest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	anchorPath := filepath.Join(root, "anchor.kitdb")
	anchor, err := database.CreateBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	backedUp, err := LogicalDigestBackupAnchor(context.Background(), anchorPath)
	if err != nil {
		t.Fatal(err)
	}
	restorePath := filepath.Join(root, "restored.kitdb")
	if _, err := RestoreToTransaction(
		context.Background(), anchorPath, "", restorePath, anchor.Transaction,
	); err != nil {
		t.Fatal(err)
	}
	restored, err := LogicalDigestBackupAnchor(context.Background(), restorePath)
	if err != nil {
		t.Fatal(err)
	}
	if live != backedUp || live != restored || live.Format != LogicalDigestFormat ||
		live.DatabaseID != anchor.DatabaseID || live.Transaction != anchor.Transaction ||
		live.Records != 2 || live.Bytes != 8 || live.SHA256 == "" {
		t.Fatalf("digests = live=%#v backup=%#v restored=%#v", live, backedUp, restored)
	}
}

func TestLogicalDigestBindsContentAndHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.kitdb")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	commitPut(t, database, "a", "one")
	before, err := database.LogicalDigest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	commitPut(t, database, "a", "changed")
	after, err := database.LogicalDigest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.SHA256 == after.SHA256 || before.Transaction == after.Transaction {
		t.Fatalf("digest did not bind content: before=%#v after=%#v", before, after)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := database.LogicalDigest(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled digest error = %v", err)
	}
}
