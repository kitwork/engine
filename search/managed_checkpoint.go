package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	managedCheckpointFilename = "projection.checkpoint"
	managedCheckpointMaxBytes = 64 << 10
)

// ReadCheckpoint returns the caller-owned opaque checkpoint last published
// for an index. A missing checkpoint is reported as a nil payload. The search
// engine never interprets this data; projection owners validate their own
// version, source identity, and checksum.
func (manager *Manager) ReadCheckpoint(
	ctx context.Context,
	key string,
	schema Schema,
) ([]byte, error) {
	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return nil, err
	}
	defer releaseManaged()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	managed.checkpointMu.Lock()
	defer managed.checkpointMu.Unlock()
	path := filepath.Join(managed.directory, managedCheckpointFilename)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > managedCheckpointMaxBytes {
		return nil, fmt.Errorf("search: projection checkpoint has invalid size %d", stat.Size())
	}
	payload := make([]byte, int(stat.Size()))
	if _, err := io.ReadFull(file, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// WriteCheckpoint durably replaces one opaque projection checkpoint after the
// caller's index mutation commit. If a process stops before this publication,
// the previous checkpoint remains authoritative and replay must be idempotent.
func (manager *Manager) WriteCheckpoint(
	ctx context.Context,
	key string,
	schema Schema,
	payload []byte,
) error {
	if ctx == nil {
		return fmt.Errorf("search: checkpoint context is nil")
	}
	if len(payload) == 0 || len(payload) > managedCheckpointMaxBytes {
		return fmt.Errorf("search: projection checkpoint must be between 1 and %d bytes", managedCheckpointMaxBytes)
	}
	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return err
	}
	defer releaseManaged()
	if managed.currentInfo().Generation == 0 {
		return ErrIndexNotFound
	}
	managed.checkpointMu.Lock()
	defer managed.checkpointMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	path := filepath.Join(managed.directory, managedCheckpointFilename)
	temporary, err := temporarySegmentPath(path)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	keepTemporary := true
	defer func() {
		_ = file.Close()
		if keepTemporary {
			_ = os.Remove(temporary)
		}
	}()
	written, err := io.Copy(file, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if written != int64(len(payload)) {
		return io.ErrShortWrite
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := replaceFile(temporary, path); err != nil {
		return err
	}
	keepTemporary = false
	if err := syncDirectory(managed.directory); err != nil {
		return durabilityUncertain(err)
	}
	return nil
}
