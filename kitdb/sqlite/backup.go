package sqlite

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Backup writes to target a copy of the database as of its latest commit and returns that revision
// (KITWORK_KITDB.md câu 13.11 (b): backup is a function of KitDB; a command or a schedule calls it). The copy is every
// page of the file, read through sqlite_dbpage in one read transaction of its own, so the writer goes on while it is
// taken: a SQLite file, which Open (or any SQLite) opens as it is, with its history up to that revision. The anchors
// beside the file do not survive the loss of the disk they are on; a copy on another disk does.
//
// target must not exist; its directory is made. The pages are written beside it under another name, synced, read back
// (the copy's newest revision must be the one it was taken at), then renamed to target: target is a whole copy, or is
// not there.
func (database *Database) Backup(ctx context.Context, target string) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	if ctx == nil || target == "" {
		return 0, fmt.Errorf("kitdb: backup needs a context and a target")
	}
	target = filepath.Clean(target)
	if _, err := os.Stat(target); err == nil {
		return 0, fmt.Errorf("kitdb: backup target %q exists", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if absolute, err := filepath.Abs(target); err == nil {
		if own, err := filepath.Abs(database.path); err == nil && absolute == own {
			return 0, fmt.Errorf("kitdb: backup target %q is the database itself", target)
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	source, err := sql.Open("sqlite", dataSourceName(database.path, "mode=ro"))
	if err != nil {
		return 0, err
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	tx, err := source.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var revision, pageSize, pageCount int64
	err = tx.QueryRowContext(ctx, `SELECT rev FROM kitwork_history ORDER BY rev DESC LIMIT 1`).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return 0, err
	}

	temporary := filepath.Join(filepath.Dir(target), fmt.Sprintf(".%s.backup-%d.tmp", filepath.Base(target), time.Now().UnixNano()))
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	written := false
	defer func() {
		if !written {
			_ = file.Close()
			_ = os.Remove(temporary)
		}
	}()
	output := bufio.NewWriterSize(file, 1<<20)
	rows, err := tx.QueryContext(ctx, `SELECT pgno, data FROM sqlite_dbpage`)
	if err != nil {
		return 0, err
	}
	next := int64(1)
	empty := make([]byte, pageSize)
	for rows.Next() {
		var number int64
		var data sql.RawBytes
		if err := rows.Scan(&number, &data); err != nil {
			rows.Close()
			return 0, err
		}
		if number < next || number > pageCount || int64(len(data)) != pageSize {
			rows.Close()
			return 0, fmt.Errorf("kitdb: sqlite_dbpage gave page %d of %d bytes after page %d of %d", number, len(data), next-1, pageCount)
		}
		for ; next < number; next++ { // a page sqlite_dbpage does not give holds nothing
			if _, err := output.Write(empty); err != nil {
				rows.Close()
				return 0, err
			}
		}
		if _, err := output.Write(data); err != nil {
			rows.Close()
			return 0, err
		}
		next++
		if next%4096 == 0 {
			if err := ctx.Err(); err != nil {
				rows.Close()
				return 0, err
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for ; next <= pageCount; next++ {
		if _, err := output.Write(empty); err != nil {
			return 0, err
		}
	}
	if err := output.Flush(); err != nil {
		return 0, err
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	_ = tx.Rollback()

	// read back: the copy opens and its history ends where it was taken
	copied, err := sql.Open("sqlite", dataSourceName(temporary, "mode=ro"))
	if err != nil {
		return 0, err
	}
	var last int64
	err = copied.QueryRowContext(ctx, `SELECT coalesce(max(rev), 0) FROM kitwork_history`).Scan(&last)
	copied.Close()
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(temporary + suffix)
	}
	if err != nil {
		return 0, fmt.Errorf("kitdb: the backup does not read back: %w", err)
	}
	if last != revision {
		return 0, fmt.Errorf("kitdb: the backup reads back at revision %d, taken at %d", last, revision)
	}
	if err := os.Rename(temporary, target); err != nil {
		return 0, err
	}
	written = true
	if directory, err := os.Open(filepath.Dir(target)); err == nil {
		_ = directory.Sync() // the rename itself on disk, where the system allows it
		_ = directory.Close()
	}
	return uint64(revision), nil
}
