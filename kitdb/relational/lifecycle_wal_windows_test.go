package relational

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

// Exercise a real Windows no-delete-share blocker, not a successful retry loop
// or an injected errno. A recoverable publication error must remain observable.
func TestLifecycleWindowsWALReplaceDenial(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit_checkpoint_retry_%t", retry), func(t *testing.T) {
			before, err := sampleLifecycleResources()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "blocked.kitdb")
			manager, err := kitdbnode.NewManager(kitdbnode.Limits{MaxOpenDatabases: 1, MaxConcurrentMaintenance: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			lease, err := manager.Acquire(ctx, path, kitdbengine.OpenOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			engine, err := Attach(lease.DB(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			execute := func(query string) {
				t.Helper()
				if _, err := engine.Execute(ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			execute("CREATE TABLE items(id BIGINT PRIMARY KEY, n BIGINT NOT NULL)")
			execute("CREATE INDEX items_n ON items(n)")
			if _, err := lease.DB().Checkpoint(); err != nil {
				t.Fatal(err)
			}
			execute("INSERT INTO items VALUES(1,10)")
			walName, err := syscall.UTF16PtrFromString(path + ".wal")
			if err != nil {
				t.Fatal(err)
			}
			blocker, err := syscall.CreateFile(walName, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if blocker != syscall.InvalidHandle {
					if err := syscall.CloseHandle(blocker); err != nil {
						t.Error(err)
					}
				}
			}()
			ticket, err := manager.ScheduleCheckpoint(ctx, path, kitdbengine.OpenOptions{}, kitdbnode.MaintenanceNormal)
			if err != nil {
				t.Fatal(err)
			}
			_, publicationErr := ticket.Wait(ctx)
			if !errors.Is(publicationErr, syscall.ERROR_ACCESS_DENIED) && !errors.Is(publicationErr, syscall.Errno(32)) {
				t.Fatalf("expected real Windows replacement denial, got %v", publicationErr)
			}
			if errors.Is(publicationErr, kitdbengine.ErrDurabilityUncertain) || errors.Is(publicationErr, kitdbengine.ErrUnavailable) {
				t.Fatalf("recoverable replacement failed closed incorrectly: %v", publicationErr)
			}
			if manager.Stats().MaintenanceFailures != 1 {
				t.Fatal("publication failure was hidden or retried")
			}
			if err := syscall.CloseHandle(blocker); err != nil {
				t.Fatal(err)
			}
			blocker = syscall.InvalidHandle
			execute("INSERT INTO items VALUES(2,20)")
			check := func(current *Engine) {
				t.Helper()
				if err := current.database.Verify(); err != nil {
					t.Fatal(err)
				}
				result, err := current.Execute(ctx, "SELECT id,n FROM items ORDER BY id")
				want := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
				if err != nil || !reflect.DeepEqual(result.Rows, want) {
					t.Fatalf("acknowledged model=%v err=%v", result.Rows, err)
				}
				for id := int64(1); id <= 2; id++ {
					result, err := current.Execute(ctx, "SELECT id,n FROM items WHERE n=$1", id*10)
					if err != nil || !reflect.DeepEqual(result.Rows, want[id-1:id]) {
						t.Fatalf("index model=%v err=%v", result.Rows, err)
					}
				}
			}
			check(engine)
			if retry {
				ticket, err := manager.ScheduleCheckpoint(ctx, path, kitdbengine.OpenOptions{}, kitdbnode.MaintenanceNormal)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := ticket.Wait(ctx); err != nil {
					t.Fatal(err)
				}
			}
			stats, err := lease.DB().Stats()
			if err != nil || mixedDrainOf(stats) != (mixedDrain{}) {
				t.Fatalf("kernel drain=%+v err=%v", stats, err)
			}
			if err := errors.Join(engine.Close(), lease.Release(), manager.CloseContext(ctx)); err != nil {
				t.Fatal(err)
			}
			closed := manager.Stats()
			if !mixedManagerReleased(closed) || closed.MaintenanceFailures != 1 || closed.LastCloseError != "" {
				t.Fatalf("close evidence=%+v", closed)
			}
			independent, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer independent.Close()
			check(independent)
			if err := independent.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := sampleLifecycleResources()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("replacement_errno=%v explicit_retry=%t acknowledged_rows=2 independent_reopen=true failed_maintenance=1 owned_resources_released=true handles=%d->%d threads=%d->%d", publicationErr, retry, before.Handles, after.Handles, before.Threads, after.Threads)
		})
	}
}
