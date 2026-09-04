package node

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
)

func TestManagerSharesHandleAndReleasesIdleOwnership(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t, Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 4 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
	})
	path := filepath.Join(root, "tenant.kitdb")

	first, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Acquire(context.Background(), filepath.Join(filepath.Dir(path), ".", filepath.Base(path)), kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.DB() == nil || first.DB() != second.DB() {
		t.Fatal("canonical path did not share one database handle")
	}
	stats := manager.Stats()
	if stats.ManagedDatabases != 1 || stats.ActiveDatabases != 1 || stats.ActiveLeases != 2 ||
		stats.ReservedPageCacheBytes != 1<<20 || stats.Opens != 1 || stats.Reuses != 1 {
		t.Fatalf("active stats = %#v", stats)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if first.DB() != nil {
		t.Fatal("released lease still exposes its database")
	}
	if err := first.Release(); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	stats = manager.Stats()
	if stats.IdleDatabases != 1 || stats.ActiveLeases != 0 {
		t.Fatalf("idle stats = %#v", stats)
	}
	if _, err := kitdb.Open(path); !errors.Is(err, kitdb.ErrWriterLocked) {
		t.Fatalf("idle managed handle did not retain exclusive ownership: %v", err)
	}

	closed, err := manager.TrimIdle(context.Background(), 0)
	if err != nil || closed != 1 {
		t.Fatalf("TrimIdle = (%d, %v), want (1, nil)", closed, err)
	}
	direct, err := kitdb.Open(path)
	if err != nil {
		t.Fatalf("trimmed database remained locked: %v", err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerEvictsLeastRecentlyUsedIdleDatabase(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t, Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
	})
	pathA := filepath.Join(root, "a.kitdb")
	pathB := filepath.Join(root, "b.kitdb")
	pathC := filepath.Join(root, "c.kitdb")

	acquireAndRelease(t, manager, pathA, kitdb.OpenOptions{})
	acquireAndRelease(t, manager, pathB, kitdb.OpenOptions{})
	acquireAndRelease(t, manager, pathA, kitdb.OpenOptions{})
	leaseC, err := manager.Acquire(context.Background(), pathC, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer leaseC.Release()

	directB, err := kitdb.Open(pathB)
	if err != nil {
		t.Fatalf("oldest idle database was not evicted: %v", err)
	}
	if err := directB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := kitdb.Open(pathA); !errors.Is(err, kitdb.ErrWriterLocked) {
		t.Fatalf("recent idle database was evicted instead of B: %v", err)
	}
	stats := manager.Stats()
	if stats.ManagedDatabases != 2 || stats.Evictions != 1 || stats.Opens != 3 || stats.Closes != 1 {
		t.Fatalf("LRU stats = %#v", stats)
	}
}

func TestManagerWarmDatabaseSurvivesCapacityEvictionAndTrim(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t, Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 2 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
	})
	warmPath := filepath.Join(root, "warm.kitdb")
	coldPath := filepath.Join(root, "cold.kitdb")
	nextPath := filepath.Join(root, "next.kitdb")

	warm, err := manager.AcquireWithPolicy(
		context.Background(), warmPath, kitdb.OpenOptions{}, DatabasePolicy{Warm: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := warm.Release(); err != nil {
		t.Fatal(err)
	}
	acquireAndRelease(t, manager, coldPath, kitdb.OpenOptions{})

	next, err := manager.Acquire(context.Background(), nextPath, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}

	directCold, err := kitdb.Open(coldPath)
	if err != nil {
		t.Fatalf("cold database was not evicted: %v", err)
	}
	if err := directCold.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := kitdb.Open(warmPath); !errors.Is(err, kitdb.ErrWriterLocked) {
		t.Fatalf("warm database lost managed ownership: %v", err)
	}

	closed, err := manager.TrimIdle(context.Background(), 0)
	if err != nil || closed != 1 {
		t.Fatalf("TrimIdle with one warm database = (%d, %v), want (1, nil)", closed, err)
	}
	stats := manager.Stats()
	if stats.ManagedDatabases != 1 || stats.IdleDatabases != 1 ||
		stats.WarmDatabases != 1 || stats.WarmIdleDatabases != 1 ||
		stats.WarmReservedPageCacheBytes != 1<<20 {
		t.Fatalf("warm stats = %#v", stats)
	}

	if err := manager.SetDatabasePolicy(warmPath, DatabasePolicy{}); err != nil {
		t.Fatal(err)
	}
	closed, err = manager.TrimIdle(context.Background(), 0)
	if err != nil || closed != 1 {
		t.Fatalf("TrimIdle after removing warm policy = (%d, %v), want (1, nil)", closed, err)
	}
}

func TestManagerWarmCapacityFailsWithoutUnboundedWait(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t, Limits{
		MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
	})
	warmPath := filepath.Join(root, "warm.kitdb")

	warm, err := manager.AcquireWithPolicy(
		context.Background(), warmPath, kitdb.OpenOptions{}, DatabasePolicy{Warm: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := warm.Release(); err != nil {
		t.Fatal(err)
	}

	_, err = manager.Acquire(context.Background(), filepath.Join(root, "next.kitdb"), kitdb.OpenOptions{})
	if !errors.Is(err, ErrWarmCapacity) {
		t.Fatalf("warm capacity error = %v", err)
	}
	if stats := manager.Stats(); stats.WaitingAcquisitions != 0 || stats.Waits != 0 {
		t.Fatalf("impossible warm acquisition waited: %#v", stats)
	}
	if err := manager.SetDatabasePolicy(filepath.Join(root, "missing.kitdb"), DatabasePolicy{}); !errors.Is(err, ErrDatabaseNotManaged) {
		t.Fatalf("unmanaged policy error = %v", err)
	}
}

func TestManagerExplicitPolicyMustMatchManagedHandle(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t, Limits{
		MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
	})
	path := filepath.Join(root, "tenant.kitdb")
	first, err := manager.AcquireWithPolicy(
		context.Background(), path, kitdb.OpenOptions{}, DatabasePolicy{Warm: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := manager.AcquireWithPolicy(
		context.Background(), path, kitdb.OpenOptions{}, DatabasePolicy{},
	); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("policy mismatch error = %v", err)
	}
	ordinary, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatalf("ordinary acquire changed policy: %v", err)
	}
	if err := ordinary.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerAppliesCancelableBackpressureWhileAllHandlesAreLeased(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t, Limits{
		MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
	})
	active, err := manager.Acquire(context.Background(), filepath.Join(root, "active.kitdb"), kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		lease *Lease
		err   error
	}
	waiting := make(chan result, 1)
	go func() {
		lease, acquireErr := manager.Acquire(context.Background(), filepath.Join(root, "next.kitdb"), kitdb.OpenOptions{})
		waiting <- result{lease: lease, err: acquireErr}
	}()
	waitForNodeStats(t, manager, func(stats Stats) bool { return stats.WaitingAcquisitions == 1 })
	select {
	case result := <-waiting:
		t.Fatalf("capacity waiter completed while active lease was pinned: %#v", result)
	case <-time.After(20 * time.Millisecond):
	}
	if err := active.Release(); err != nil {
		t.Fatal(err)
	}
	acquired := <-waiting
	if acquired.err != nil || acquired.lease == nil {
		t.Fatalf("capacity waiter = %#v", acquired)
	}
	if err := acquired.lease.Release(); err != nil {
		t.Fatal(err)
	}

	blocked, err := manager.Acquire(context.Background(), filepath.Join(root, "blocked.kitdb"), kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() {
		_, acquireErr := manager.Acquire(ctx, filepath.Join(root, "canceled.kitdb"), kitdb.OpenOptions{})
		canceled <- acquireErr
	}()
	waitForNodeStats(t, manager, func(stats Stats) bool { return stats.WaitingAcquisitions == 1 })
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Acquire error = %v", err)
	}
	if err := blocked.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerOpenOptionsAreStableWhileLeasedAndReplaceableWhenIdle(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t, Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 4 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
	})
	path := filepath.Join(root, "tenant.kitdb")
	first, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{PageCacheBytes: 2 << 20}); !errors.Is(err, ErrOptionsMismatch) {
		t.Fatalf("active option mismatch error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	reopened, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{PageCacheBytes: 2 << 20})
	if err != nil {
		t.Fatalf("idle option replacement: %v", err)
	}
	stats, err := reopened.DB().Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.PageCacheLimitBytes != 2<<20 {
		t.Fatalf("reopened page cache = %d, want %d", stats.PageCacheLimitBytes, 2<<20)
	}
	if err := reopened.Release(); err != nil {
		t.Fatal(err)
	}
	managerStats := manager.Stats()
	if managerStats.Opens != 2 || managerStats.Closes != 1 || managerStats.ReservedPageCacheBytes != 2<<20 {
		t.Fatalf("option replacement stats = %#v", managerStats)
	}
}

func TestManagerRejectsSingleDatabaseBeyondPageCacheBudget(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t, Limits{
		MaxOpenDatabases: 2, MaxPageCacheBytes: 1 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
	})
	_, err := manager.Acquire(context.Background(), filepath.Join(root, "large.kitdb"), kitdb.OpenOptions{
		PageCacheBytes: 2 << 20,
	})
	if !errors.Is(err, ErrPageCacheBudget) {
		t.Fatalf("oversized page-cache error = %v", err)
	}
	if stats := manager.Stats(); stats.ManagedDatabases != 0 || stats.ReservedPageCacheBytes != 0 {
		t.Fatalf("rejected acquisition reserved resources: %#v", stats)
	}
}

func TestManagerConcurrentAcquirePublishesOneHandle(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t, Limits{
		MaxOpenDatabases: 4, MaxPageCacheBytes: 4 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 4,
	})
	path := filepath.Join(root, "shared.kitdb")
	const goroutines = 32
	type result struct {
		lease *Lease
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, goroutines)
	var wait sync.WaitGroup
	for range goroutines {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
			results <- result{lease: lease, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	var database *kitdb.DB
	leases := make([]*Lease, 0, goroutines)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if database == nil {
			database = result.lease.DB()
		} else if result.lease.DB() != database {
			t.Fatal("concurrent acquire published multiple handles")
		}
		leases = append(leases, result.lease)
	}
	if stats := manager.Stats(); stats.Opens != 1 || stats.ActiveLeases != goroutines || stats.Acquisitions != goroutines {
		t.Fatalf("concurrent stats = %#v", stats)
	}
	for _, lease := range leases {
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagerCloseDrainsActiveLeaseAndStopsAdmission(t *testing.T) {
	manager, err := NewManager(Limits{
		MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20,
		DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	waitForNodeStats(t, manager, func(stats Stats) bool {
		return stats.Closed && stats.ActiveLeases == 1
	})
	if _, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Acquire during close error = %v", err)
	}
	select {
	case err := <-closed:
		t.Fatalf("Close returned before active lease drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if stats := manager.Stats(); stats.ManagedDatabases != 0 || stats.ActiveLeases != 0 {
		t.Fatalf("closed stats = %#v", stats)
	}
	direct, err := kitdb.Open(path)
	if err != nil {
		t.Fatalf("Close left database locked: %v", err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerLimitValidation(t *testing.T) {
	tests := []Limits{
		{MaxOpenDatabases: -1},
		{MaxOpenDatabases: 1, MaxConcurrentOpens: 2},
		{MaxOpenDatabases: 1, MaxPageCacheBytes: -1},
		{MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20, DefaultPageCacheBytes: 2 << 20},
		{
			MaxOpenDatabases: 1, MaxPageCacheBytes: 1 << 20,
			DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 1,
			MaxConcurrentMaintenance: 2,
		},
		{MaxQueuedMaintenance: -1},
		{MaxQueuedMaintenance: 1, MaxMaintenancePerDatabase: 2},
		{MaintenanceTimeout: -time.Second},
		{ReplicaFileLinks: ReplicaFileLinkLimits{MaxLinks: -1}},
		{
			MaxConcurrentMaintenance: 1,
			ReplicaFileLinks:         ReplicaFileLinkLimits{MaxConcurrent: 2},
		},
		{ReplicaFileLinks: ReplicaFileLinkLimits{ActiveInterval: time.Microsecond}},
		{ReplicaFileLinks: ReplicaFileLinkLimits{
			ActiveInterval: time.Second, IdleInterval: time.Millisecond,
		}},
		{ReplicaFileLinks: ReplicaFileLinkLimits{
			InitialBackoff: time.Second, MaxBackoff: time.Millisecond,
		}},
	}
	for _, limits := range tests {
		if manager, err := NewManager(limits); err == nil {
			_ = manager.Close()
			t.Fatalf("NewManager(%+v) succeeded", limits)
		}
	}
}

func newTestManager(t *testing.T, limits Limits) *Manager {
	t.Helper()
	manager, err := NewManager(limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	})
	return manager
}

func acquireAndRelease(t *testing.T, manager *Manager, path string, options kitdb.OpenOptions) {
	t.Helper()
	lease, err := manager.Acquire(context.Background(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func waitForNodeStats(t *testing.T, manager *Manager, ready func(Stats) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ready(manager.Stats()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("manager state did not converge: %#v", manager.Stats())
}
