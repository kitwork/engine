package work

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	queuestore "github.com/kitwork/engine/utilities/queue"
)

// A stored job or schedule definition (a job's retry, a schedule's retry) is what running the
// file gives, and the same entry bytes can give another definition under another compiler schema or
// after a module it imports changed. Sync skips a row whose content_hash is unchanged, so content_hash
// is the compiled artifact's cache key, which covers every bundled source and the compiler
// fingerprint. A row stored under the earlier formula (the entry file alone) is written again once; a
// row stored under the current key is left alone.

// sourceOnlyHash is the content_hash every row written before 13/10/2026 carries.
func sourceOnlyHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

func TestQueueDefinitionIsStoredUnderTheArtifactCacheKey(t *testing.T) {
	source := `import * as queue from "kitwork/queue";` + "\n" + `export const mail = queue.job((context) => {}).retry(3);`
	q := newQueueApp(t, map[string]string{".shared/queue/mail.work": source})

	handler := q.app.queueWorker().handlerFor("mail")
	if handler == nil || handler.Bytecode == nil {
		t.Fatal("job mail did not register")
	}
	cacheKey := handler.Bytecode.CacheKey()
	if cacheKey == "" || cacheKey == sourceOnlyHash(source) {
		t.Fatalf("cache key = %q, want a key that differs from the source-only hash", cacheKey)
	}

	stored := func() (string, int) {
		t.Helper()
		definitions, err := q.app.queueStore().Definitions(10, 0)
		if err != nil || len(definitions) != 1 || definitions[0].Name != "mail" {
			t.Fatalf("read the definitions: %+v %v", definitions, err)
		}
		return definitions[0].ContentHash, definitions[0].Retry
	}
	if hash, retry := stored(); hash != cacheKey || retry != 3 {
		t.Errorf("after boot: content_hash = %q, retry = %d; want %q, 3", hash, retry, cacheKey)
	}

	for _, row := range []struct {
		name, hash string
		want       int
	}{
		{"a row stored under the source-only hash is written again", sourceOnlyHash(source), 3},
		// Control: the same stale retry under the current key stays, so the row above was written
		// because of its hash, not because every sync writes.
		{"a row stored under the current key is left alone", cacheKey, 1},
	} {
		q.app.StopQueueWorker()
		err := appRuns{tenant: q.app}.Write(func(db queuestore.SQL) error {
			_, err := db.Exec(`UPDATE kitwork_definitions SET content_hash = $1, retry = 1 WHERE name = 'mail'`, row.hash)
			return err
		})
		if err != nil {
			t.Fatalf("%s: %v", row.name, err)
		}
		q.app.StartQueueWorker()
		if _, retry := stored(); retry != row.want {
			t.Errorf("%s: retry = %d, want %d", row.name, retry, row.want)
		}
	}
	q.app.StopQueueWorker()
}

// A schedule's definition is the same row of kitwork_definitions, of kind schedule: the same rule.
func TestScheduleDefinitionIsStoredUnderTheArtifactCacheKey(t *testing.T) {
	source := `import * as schedule from "kitwork/schedule";` + "\n" + `export const beat = schedule.every("1h").retry(3).run((context) => {});`
	q := newQueueApp(t, map[string]string{".schedule/beat.work": source})

	handler := q.app.scheduleWorker().handlerFor("beat")
	if handler == nil || handler.Bytecode == nil {
		t.Fatal("schedule beat did not register")
	}
	cacheKey := handler.Bytecode.CacheKey()
	if cacheKey == "" || cacheKey == sourceOnlyHash(source) {
		t.Fatalf("cache key = %q, want a key that differs from the source-only hash", cacheKey)
	}

	stored := func() (string, int) {
		t.Helper()
		definitions, err := q.app.scheduleStore().Definitions(10, 0)
		if err != nil || len(definitions) != 1 || definitions[0].Name != "beat" {
			t.Fatalf("read the definitions: %+v %v", definitions, err)
		}
		return definitions[0].ContentHash, definitions[0].Retry
	}
	if hash, retry := stored(); hash != cacheKey || retry != 3 {
		t.Errorf("after boot: content_hash = %q, retry = %d; want %q, 3", hash, retry, cacheKey)
	}

	for _, row := range []struct {
		name, hash string
		want       int
	}{
		{"a row stored under the source-only hash is written again", sourceOnlyHash(source), 3},
		// Control: the same stale retry under the current key stays.
		{"a row stored under the current key is left alone", cacheKey, 1},
	} {
		q.app.StopScheduleWorker()
		err := appRuns{tenant: q.app}.Write(func(db queuestore.SQL) error {
			_, err := db.Exec(`UPDATE kitwork_definitions SET content_hash = $1, retry = 1 WHERE kind = 'schedule' AND name = 'beat'`, row.hash)
			return err
		})
		if err != nil {
			t.Fatalf("%s: %v", row.name, err)
		}
		q.app.StartScheduleWorker()
		if _, retry := stored(); retry != row.want {
			t.Errorf("%s: retry = %d, want %d", row.name, retry, row.want)
		}
	}
	q.app.StopScheduleWorker()
}
