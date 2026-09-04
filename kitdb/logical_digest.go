package kitdb

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
)

// LogicalDigestFormat identifies the canonical logical-row digest encoding.
// It hashes sorted entries as uvarint(key length), key, uvarint(value length),
// value. Physical generations, page layout, WAL state, and file paths are not
// part of the digest.
const LogicalDigestFormat = "kitdb-logical-digest/v1"

// LogicalDigest proves one exact logical database boundary without exposing
// keys or values.
type LogicalDigest struct {
	Format      string
	DatabaseID  string
	Transaction uint64
	Records     uint64
	Bytes       uint64
	SHA256      string
}

// LogicalDigest returns a digest of one fixed live snapshot. Concurrent commits
// may continue after the snapshot has been captured.
func (db *DB) LogicalDigest(ctx context.Context) (LogicalDigest, error) {
	if db == nil {
		return LogicalDigest{}, ErrClosed
	}
	if ctx == nil {
		return LogicalDigest{}, fmt.Errorf("kitdb: nil logical digest context")
	}
	if err := ctx.Err(); err != nil {
		return LogicalDigest{}, err
	}
	snapshot, err := db.Snapshot()
	if err != nil {
		return LogicalDigest{}, err
	}

	snapshot.mu.RLock()
	result, digestErr := logicalDigestRows(
		ctx,
		hex.EncodeToString(snapshot.main.identity[:]),
		snapshot.transaction,
		func(emit func(key, value []byte) error) error {
			return walkMergedRows(snapshot.main, snapshot.overlay, emit)
		},
	)
	snapshot.mu.RUnlock()
	return result, errors.Join(digestErr, snapshot.Close())
}

// LogicalDigestBackupAnchor verifies and digests an immutable standalone
// backup without creating a WAL, lock file, or writable database handle.
func LogicalDigestBackupAnchor(ctx context.Context, path string) (LogicalDigest, error) {
	if ctx == nil {
		return LogicalDigest{}, fmt.Errorf("kitdb: nil logical digest context")
	}
	anchor, err := VerifyBackupAnchor(ctx, path)
	if err != nil {
		return LogicalDigest{}, err
	}
	main, err := readMainSnapshotWithCache(anchor.Path, 0)
	if err != nil {
		return LogicalDigest{}, err
	}
	result, digestErr := logicalDigestRows(
		ctx,
		anchor.DatabaseID,
		anchor.Transaction,
		func(emit func(key, value []byte) error) error {
			return walkMergedRows(main, nil, emit)
		},
	)
	closeErr := main.close()
	if digestErr == nil && result.Records != anchor.Records {
		digestErr = corruptFileAt(
			anchor.Path,
			0,
			"logical digest counted %d records, backup metadata declares %d",
			result.Records,
			anchor.Records,
		)
	}
	return result, errors.Join(digestErr, closeErr)
}

func logicalDigestRows(
	ctx context.Context,
	databaseID string,
	transaction uint64,
	walk func(func(key, value []byte) error) error,
) (LogicalDigest, error) {
	result := LogicalDigest{
		Format: LogicalDigestFormat, DatabaseID: databaseID, Transaction: transaction,
	}
	hasher := sha256.New()
	err := walk(func(key, value []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var lengths [20]byte
		keyBytes := binary.PutUvarint(lengths[:10], uint64(len(key)))
		valueBytes := binary.PutUvarint(lengths[10:], uint64(len(value)))
		if err := writeLogicalDigest(hasher, lengths[:keyBytes]); err != nil {
			return err
		}
		if err := writeLogicalDigest(hasher, key); err != nil {
			return err
		}
		if err := writeLogicalDigest(hasher, lengths[10:10+valueBytes]); err != nil {
			return err
		}
		if err := writeLogicalDigest(hasher, value); err != nil {
			return err
		}
		result.Records++
		result.Bytes += uint64(len(key) + len(value))
		return nil
	})
	if err != nil {
		return LogicalDigest{}, err
	}
	result.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	return result, nil
}

func writeLogicalDigest(hasher hash.Hash, data []byte) error {
	written, err := hasher.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return fmt.Errorf("kitdb: short logical digest write: %d of %d", written, len(data))
	}
	return nil
}
