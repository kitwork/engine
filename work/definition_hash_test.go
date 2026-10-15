package work

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// A stored _queue or _cron definition (a queue's max_attempts, a cron's schedule) is what running the
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
	source := `import { queue } from "kitwork";` + "\n" + `queue.retry(3).handle(() => {});`
	tenant := newQueueTenant(t, "mail", source)
	defer tenant.Close()

	worker := tenant.queueWorker()
	if worker == nil || worker.db == nil {
		t.Fatal("queue worker has no database — the worker did not start")
	}
	handler := worker.handlerFor("mail")
	if handler == nil || handler.Bytecode == nil {
		t.Fatal("queue mail did not register")
	}
	cacheKey := handler.Bytecode.CacheKey()
	if cacheKey == "" || cacheKey == sourceOnlyHash(source) {
		t.Fatalf("cache key = %q, want a key that differs from the source-only hash", cacheKey)
	}

	stored := func() (string, int) {
		t.Helper()
		var hash string
		var maxAttempts int
		if err := worker.db.QueryRow(`SELECT content_hash, max_attempts FROM queues WHERE name='mail'`).Scan(&hash, &maxAttempts); err != nil {
			t.Fatalf("read the queues row: %v", err)
		}
		return hash, maxAttempts
	}
	if hash, maxAttempts := stored(); hash != cacheKey || maxAttempts != 3 {
		t.Errorf("after boot: content_hash = %q, max_attempts = %d; want %q, 3", hash, maxAttempts, cacheKey)
	}

	for _, row := range []struct {
		name, hash string
		want       int
	}{
		{"a row stored under the source-only hash is written again", sourceOnlyHash(source), 3},
		// Control: the same stale max_attempts under the current key stays, so the row above was
		// written because of its hash, not because every sync writes.
		{"a row stored under the current key is left alone", cacheKey, 1},
	} {
		tenant.StopQueueWorker()
		if _, err := worker.db.Exec(`UPDATE queues SET content_hash=?, max_attempts=1 WHERE name='mail'`, row.hash); err != nil {
			t.Fatalf("%s: %v", row.name, err)
		}
		tenant.StartQueueWorker()
		if _, maxAttempts := stored(); maxAttempts != row.want {
			t.Errorf("%s: max_attempts = %d, want %d", row.name, maxAttempts, row.want)
		}
	}
	tenant.StopQueueWorker()
}

func TestCronDefinitionIsStoredUnderTheArtifactCacheKey(t *testing.T) {
	tmp := t.TempDir()
	site := filepath.Join(tmp, "acme", "localhost")
	cronDir := filepath.Join(tmp, "acme", "_cron")
	for _, dir := range []string{site, cronDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(site, "router.kitwork.js"),
		[]byte(`import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`), 0644); err != nil {
		t.Fatal(err)
	}
	source := `import { cron } from "kitwork";` + "\n" + `cron.every("1h").handle(() => {});`
	if err := os.WriteFile(filepath.Join(cronDir, "beat.kitwork.js"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewAppTenant(tmp, "acme")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	scheduler := tenant.scheduler()
	if scheduler == nil || scheduler.db == nil {
		t.Fatal("scheduler has no database — it did not start")
	}
	cacheKey := ""
	scheduler.mu.Lock()
	for _, job := range scheduler.jobs {
		if job.Name == "beat" && job.Bytecode != nil {
			cacheKey = job.Bytecode.CacheKey()
		}
	}
	scheduler.mu.Unlock()
	if cacheKey == "" || cacheKey == sourceOnlyHash(source) {
		t.Fatalf("cache key = %q, want a key that differs from the source-only hash", cacheKey)
	}

	stored := func() (string, string) {
		t.Helper()
		var hash, schedule string
		if err := scheduler.db.QueryRow(`SELECT content_hash, schedule FROM crons WHERE name='beat'`).Scan(&hash, &schedule); err != nil {
			t.Fatalf("read the crons row: %v", err)
		}
		return hash, schedule
	}
	if hash, schedule := stored(); hash != cacheKey || schedule != "@every 1h" {
		t.Errorf("after boot: content_hash = %q, schedule = %q; want %q, @every 1h", hash, schedule, cacheKey)
	}

	for _, row := range []struct {
		name, hash, want string
	}{
		{"a row stored under the source-only hash is written again", sourceOnlyHash(source), "@every 1h"},
		// Control: the same stale schedule under the current key stays.
		{"a row stored under the current key is left alone", cacheKey, "@every 9h"},
	} {
		tenant.StopCronJobs()
		if _, err := scheduler.db.Exec(`UPDATE crons SET content_hash=?, schedule='@every 9h' WHERE name='beat'`, row.hash); err != nil {
			t.Fatalf("%s: %v", row.name, err)
		}
		tenant.StartCronJobs()
		if _, schedule := stored(); schedule != row.want {
			t.Errorf("%s: schedule = %q, want %q", row.name, schedule, row.want)
		}
	}
	tenant.StopCronJobs()
}
