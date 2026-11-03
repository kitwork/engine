package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// Retention is how long a Database keeps what restores a past moment, and how
// often it anchors (KITWORK_KITDB.md mục 5, câu 13.6).
type Retention struct {
	// Keep is the history kept: every moment from now - Keep on restores, and
	// the history holds every commit since.
	Keep time.Duration
	// Every anchors the file once the oldest commit no anchor holds is this old,
	// so a restore replays at most about that much history and what Keep lets
	// go of goes anchor by anchor.
	Every time.Duration
}

// pruneBatch is how many revisions one Maintain takes out of kitwork_history,
// so a large prune never holds the writer for long.
const pruneBatch = 10000

// Retain applies retention as of now. It writes an anchor when the oldest
// commit no anchor holds is Every old, then removes what no moment from
// now - Keep on needs: the anchors older than the newest one at or before that
// moment, and the commits of kitwork_history before that anchor's revision. The
// anchor's own commit stays, so the history starts at the moment the oldest
// kept anchor holds. It returns that revision: the commits before it are gone,
// 0 when every commit is still kept.
func (database *Database) Retain(ctx context.Context, now time.Time, retention Retention) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	if retention.Keep <= 0 || retention.Every <= 0 {
		return 0, fmt.Errorf("kitdb: retention must be positive")
	}
	anchors, err := database.anchors()
	if err != nil {
		return 0, err
	}
	var newest uint64
	if len(anchors) > 0 {
		newest = anchors[len(anchors)-1].revision
	}
	var oldest int64
	if err := database.readers.QueryRowContext(ctx, `SELECT coalesce(min(at), 0) FROM kitwork_history WHERE rev > ?`,
		int64(newest)).Scan(&oldest); err != nil {
		return 0, err
	}
	if oldest != 0 && now.Sub(time.Unix(0, oldest)) >= retention.Every {
		if _, err := database.Anchor(ctx); err != nil {
			return 0, err
		}
		if anchors, err = database.anchors(); err != nil {
			return 0, err
		}
	}
	from := now.Add(-retention.Keep).UTC().UnixNano()
	var start *anchor
	for position := range anchors {
		if anchors[position].at <= from {
			start = &anchors[position]
		}
	}
	if start == nil || start.revision == 0 {
		return 0, nil
	}
	// The anchors go first: a history that still holds commits an anchor no longer starts is only longer than it
	// needs to be, and the next Retain takes them out.
	var errs []error
	for _, older := range anchors {
		if older.revision < start.revision {
			if err := os.Remove(older.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	var first int64
	if err := database.readers.QueryRowContext(ctx, `SELECT coalesce(min(rev), 0) FROM kitwork_history`).Scan(&first); err != nil {
		return 0, errors.Join(append(errs, err)...)
	}
	for low := first; low > 0 && low < int64(start.revision); low += pruneBatch {
		below := min(low+pruneBatch, int64(start.revision))
		if _, err := database.Maintain(ctx, "retention", func(conn *sql.Conn) error {
			_, err := conn.ExecContext(ctx, `DELETE FROM kitwork_history WHERE rev < ?`, below)
			return err
		}); err != nil {
			return 0, errors.Join(append(errs, err)...)
		}
	}
	return start.revision, errors.Join(errs...)
}

// KeptFrom is the moment the history starts at once Retain has removed
// commits from it: the commit of its first kept revision. kept is false while
// the history still holds every commit from the first.
func (database *Database) KeptFrom(ctx context.Context) (time.Time, bool, error) {
	if err := database.ensureOpen(); err != nil {
		return time.Time{}, false, err
	}
	at, pruned, err := database.keptFrom(ctx)
	return time.Unix(0, at).UTC(), pruned, err
}

func (database *Database) keptFrom(ctx context.Context) (int64, bool, error) {
	var revision, at int64
	err := database.readers.QueryRowContext(ctx, `SELECT rev, at FROM kitwork_history ORDER BY rev LIMIT 1`).Scan(&revision, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return at, revision > 1, err
}

// prunedBefore is ErrPruned for a moment before the history kept.
func (database *Database) prunedBefore(ctx context.Context, through int64) error {
	at, pruned, err := database.keptFrom(ctx)
	if err != nil {
		return err
	}
	if pruned && through < at {
		return fmt.Errorf("%w: it starts at %s", ErrPruned, time.Unix(0, at).UTC().Format(time.RFC3339Nano))
	}
	return nil
}
