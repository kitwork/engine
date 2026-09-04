package kitdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	backupAnchorGeneration = 1
	backupHashBufferSize   = 256 << 10
)

// BackupAnchor describes one immutable, standalone KitDB main file. SHA256
// identifies the exact published bytes; BoundaryChecksum links the anchor's
// transaction to subsequent retained-history frames.
type BackupAnchor struct {
	Path             string
	DatabaseID       string
	FormatVersion    uint16
	Generation       uint64
	Transaction      uint64
	BoundaryChecksum uint32
	Records          uint64
	Bytes            int64
	SHA256           string
}

// Cursor returns the exact source boundary protected by this anchor.
func (anchor BackupAnchor) Cursor() HistoryCursor {
	return HistoryCursor{
		DatabaseID:  anchor.DatabaseID,
		Transaction: anchor.Transaction,
		Checksum:    anchor.BoundaryChecksum,
	}
}

// CreateBackupAnchor writes the latest committed logical state to a new,
// compacted format-v3 main file. Commits may continue while the anchor is
// built. The destination must not already exist and is published only after
// every page and the exact file digest have been verified.
func (db *DB) CreateBackupAnchor(ctx context.Context, path string) (BackupAnchor, error) {
	if ctx == nil {
		return BackupAnchor{}, fmt.Errorf("kitdb: nil backup context")
	}
	destination, err := resolveBackupAnchorPath(path)
	if err != nil {
		return BackupAnchor{}, err
	}
	if sameBackupPath(destination, db.path) {
		return BackupAnchor{}, fmt.Errorf("kitdb: backup destination is the live database file")
	}
	if err := validateBackupDestination(destination); err != nil {
		return BackupAnchor{}, err
	}
	if err := ctx.Err(); err != nil {
		return BackupAnchor{}, err
	}

	snapshot, err := db.Snapshot()
	if err != nil {
		return BackupAnchor{}, err
	}
	anchor, createErr := createBackupAnchorFromSnapshot(ctx, snapshot, destination)
	return anchor, errors.Join(createErr, snapshot.Close())
}

// VerifyBackupAnchor validates the complete standalone main file, rejects
// uncommitted trailing bytes, and returns a SHA-256 digest of its exact bytes.
// It does not create a WAL, lock file, or database handle.
func VerifyBackupAnchor(ctx context.Context, path string) (BackupAnchor, error) {
	if ctx == nil {
		return BackupAnchor{}, fmt.Errorf("kitdb: nil backup context")
	}
	resolved, err := resolveBackupAnchorPath(path)
	if err != nil {
		return BackupAnchor{}, err
	}
	return verifyBackupAnchorPath(ctx, resolved)
}

func createBackupAnchorFromSnapshot(ctx context.Context, snapshot *Snapshot, destination string) (BackupAnchor, error) {
	if snapshot == nil {
		return BackupAnchor{}, ErrSnapshotClosed
	}
	if err := ctx.Err(); err != nil {
		return BackupAnchor{}, err
	}

	var identity [16]byte
	var transaction uint64
	var boundaryChecksum uint32
	stagingPath, err := func() (string, error) {
		snapshot.mu.RLock()
		defer snapshot.mu.RUnlock()
		if snapshot.closed || snapshot.main == nil {
			return "", ErrSnapshotClosed
		}
		identity = snapshot.main.identity
		transaction = snapshot.transaction
		boundaryChecksum = snapshot.boundaryChecksum
		return prepareCompactedGeneration(
			destination,
			identity,
			transaction,
			boundaryChecksum,
			backupAnchorGeneration,
			func(emit func(key, value []byte) error) error {
				return walkMergedRows(snapshot.main, snapshot.overlay, func(key, value []byte) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					return emit(key, value)
				})
			},
		)
	}()
	if err != nil {
		return BackupAnchor{}, err
	}
	defer os.Remove(stagingPath)

	anchor, err := verifyBackupAnchorPath(ctx, stagingPath)
	if err != nil {
		return BackupAnchor{}, err
	}
	expectedGeneration := uint64(backupAnchorGeneration)
	if transaction == 0 {
		expectedGeneration = 0
	}
	if anchor.DatabaseID != hex.EncodeToString(identity[:]) ||
		anchor.FormatVersion != mainFormatVersion ||
		anchor.Generation != expectedGeneration ||
		anchor.Transaction != transaction ||
		anchor.BoundaryChecksum != boundaryChecksum {
		return BackupAnchor{}, corruptFileAt(stagingPath, 0, "prepared backup anchor does not match its captured snapshot")
	}
	if err := ctx.Err(); err != nil {
		return BackupAnchor{}, err
	}
	if err := publishBackupAnchor(stagingPath, destination); err != nil {
		return BackupAnchor{}, err
	}
	anchor.Path = destination
	return anchor, nil
}

func verifyBackupAnchorPath(ctx context.Context, path string) (BackupAnchor, error) {
	if err := ctx.Err(); err != nil {
		return BackupAnchor{}, err
	}
	main, err := readMainSnapshotWithCache(path, 0)
	if err != nil {
		return BackupAnchor{}, err
	}
	defer main.close()

	info, err := main.file.Stat()
	if err != nil {
		return BackupAnchor{}, fmt.Errorf("kitdb: stat backup anchor: %w", err)
	}
	if info.Size() != main.fileEnd {
		return BackupAnchor{}, corruptFileAt(path, main.fileEnd, "backup anchor size is %d, want exact active boundary %d", info.Size(), main.fileEnd)
	}
	if err := main.verifyContext(ctx); err != nil {
		return BackupAnchor{}, err
	}
	digest, err := hashBackupAnchor(ctx, path, main.fileEnd)
	if err != nil {
		return BackupAnchor{}, err
	}
	return BackupAnchor{
		Path:             path,
		DatabaseID:       hex.EncodeToString(main.identity[:]),
		FormatVersion:    main.formatVersion,
		Generation:       main.generation,
		Transaction:      main.transaction,
		BoundaryChecksum: main.boundaryChecksum,
		Records:          main.records,
		Bytes:            main.fileEnd,
		SHA256:           digest,
	}, nil
}

func hashBackupAnchor(ctx context.Context, path string, size int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("kitdb: open backup anchor for hashing: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	reader := &backupContextReader{ctx: ctx, reader: io.LimitReader(file, size)}
	written, err := io.CopyBuffer(hash, reader, make([]byte, backupHashBufferSize))
	if err != nil {
		return "", fmt.Errorf("kitdb: hash backup anchor: %w", err)
	}
	if written != size {
		return "", corruptFileAt(path, written, "backup anchor ended after %d of %d bytes", written, size)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type backupContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *backupContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func publishBackupAnchor(stagingPath, destination string) error {
	if err := os.Link(stagingPath, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.Join(ErrBackupExists, fmt.Errorf("kitdb: backup destination %q already exists", destination))
		}
		return fmt.Errorf("kitdb: publish backup anchor: %w", err)
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: sync backup anchor directory: %w", err))
	}
	return nil
}

func resolveBackupAnchorPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("kitdb: empty backup anchor path")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("kitdb: resolve backup anchor path: %w", err)
	}
	resolved = filepath.Clean(resolved)
	parent := filepath.Dir(resolved)
	info, err := os.Stat(parent)
	if err != nil {
		return "", fmt.Errorf("kitdb: inspect backup anchor parent: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("kitdb: backup anchor parent %q is not a directory", parent)
	}
	return resolved, nil
}

func validateBackupDestination(path string) error {
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return errors.Join(ErrBackupExists, fmt.Errorf("kitdb: backup destination %q already exists", path))
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("kitdb: inspect backup destination: %w", err)
	}
}

func sameBackupPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
