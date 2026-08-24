package collection

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	searchengine "github.com/kitwork/engine/search"
	"github.com/kitwork/engine/value"
	_ "modernc.org/sqlite"
)

type segmentCanaryTestScope struct {
	root          string
	db            *sql.DB
	searchRoot    string
	searchManager *searchengine.Manager
	canary        bool

	mu   sync.Mutex
	keys map[string]struct{}

	providerOnce    sync.Once
	providerEntered chan struct{}
	providerRelease chan struct{}
}

func (scope *segmentCanaryTestScope) AppID() string  { return "canary-test" }
func (scope *segmentCanaryTestScope) Domain() string { return "canary.test" }
func (scope *segmentCanaryTestScope) ResolvePath(paths ...string) string {
	return filepath.Join(append([]string{scope.root}, paths...)...)
}
func (scope *segmentCanaryTestScope) DB(string) *sql.DB { return scope.db }
func (scope *segmentCanaryTestScope) CollectionSearchCanaryEnabled() bool {
	return scope.canary
}
func (scope *segmentCanaryTestScope) CollectionSearchManager() (*searchengine.Manager, error) {
	if scope.providerRelease != nil {
		scope.providerOnce.Do(func() { close(scope.providerEntered) })
		<-scope.providerRelease
	}
	return scope.searchManager, nil
}
func (scope *segmentCanaryTestScope) RegisterCollectionSearchIndex(key string) {
	scope.mu.Lock()
	if scope.keys == nil {
		scope.keys = make(map[string]struct{})
	}
	scope.keys[key] = struct{}{}
	scope.mu.Unlock()
}

func newSegmentCanaryTestScope(t *testing.T, root string, db *sql.DB) *segmentCanaryTestScope {
	t.Helper()
	searchRoot := filepath.Join(root, "host-search")
	manager, err := searchengine.NewManager(searchRoot, searchengine.ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return &segmentCanaryTestScope{
		root: root, db: db, searchRoot: searchRoot, searchManager: manager,
		keys: make(map[string]struct{}),
	}
}

func TestCollectionSegmentProjectionUsesManagedStreamingReplacement(t *testing.T) {
	root := t.TempDir()
	writeCollectionDocument(t, root, "products", "ao-thun.md", "Áo thun", "Sản phẩm của Nguyễn")
	writeCollectionDocument(t, root, "products", "quan.md", "Quần jeans", "Denim")
	scope := newSegmentCanaryTestScope(t, root, nil)
	manager := NewManager(scope)
	defer manager.Close()
	handle := openCollectionTestHandle(t, manager, "products")

	projection, err := newCollectionSegmentProjection(scope, handle.collection.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(projection.signatureDirectory, filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatalf("signature path escaped tenant root: %q", projection.signatureDirectory)
	}
	index, err := handle.collection.Index()
	if err != nil {
		t.Fatal(err)
	}
	first, complete, err := projection.rebuild(
		context.Background(), handle, index, "source-one",
	)
	if err != nil || !complete {
		t.Fatalf("first replacement complete=%v err=%v", complete, err)
	}
	if first.Generation != 1 || first.Documents != 2 {
		t.Fatalf("first projection info = %#v", first)
	}
	if signature, err := projection.signature(context.Background()); err != nil || signature != "source-one" {
		t.Fatalf("first signature = %q, %v", signature, err)
	}
	hits, err := projection.search(context.Background(), "nguyen", 20)
	if err != nil || len(hits) != 1 || hits[0].ID != "ao-thun" {
		t.Fatalf("Vietnamese projection hits = %#v, %v", hits, err)
	}
	if err := os.Remove(filepath.Join(root, "_collection", "products", "quan.md")); err != nil {
		t.Fatal(err)
	}
	failed, complete, err := projection.rebuild(
		context.Background(), handle, index, "source-incomplete",
	)
	if err == nil || complete || failed.Generation != 0 {
		t.Fatalf("incomplete replacement info=%#v complete=%v err=%v", failed, complete, err)
	}
	if signature, err := projection.signature(context.Background()); err != nil || signature != "source-one" {
		t.Fatalf("failed replacement changed signature = %q, %v", signature, err)
	}
	if hits, err := projection.search(context.Background(), "nguyen", 20); err != nil || len(hits) != 1 {
		t.Fatalf("failed replacement changed live generation: %#v, %v", hits, err)
	}

	if err := os.RemoveAll(filepath.Join(root, "_collection", "products")); err != nil {
		t.Fatal(err)
	}
	writeCollectionDocument(t, root, "products", "giay.md", "Giày chạy bộ", "Bộ sưu tập mới")
	index, err = handle.collection.Index()
	if err != nil {
		t.Fatal(err)
	}
	second, complete, err := projection.rebuild(
		context.Background(), handle, index, "source-two",
	)
	if err != nil || !complete {
		t.Fatalf("second replacement complete=%v err=%v", complete, err)
	}
	if second.Generation != first.Generation+1 || second.Documents != 1 || second.PhysicalDocuments != 1 {
		t.Fatalf("second projection info = %#v", second)
	}
	if signature, err := projection.signature(context.Background()); err != nil || signature != "source-two" {
		t.Fatalf("second signature = %q, %v", signature, err)
	}
	if hits, err := projection.search(context.Background(), "nguyen", 20); err != nil || len(hits) != 0 {
		t.Fatalf("stale projection hits = %#v, %v", hits, err)
	}
	signatures, err := filepath.Glob(filepath.Join(
		projection.signatureDirectory,
		collectionSegmentSignaturePrefix+"*"+collectionSegmentSignatureSuffix,
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(signatures) != 1 || filepath.Base(signatures[0]) != collectionSegmentSignatureFilename(second.Generation) {
		t.Fatalf("projection signature files = %#v", signatures)
	}
	scope.mu.Lock()
	_, registered := scope.keys[projection.key]
	scope.mu.Unlock()
	if !registered {
		t.Fatalf("managed collection key was not registered: %q", projection.key)
	}
	if stats := scope.searchManager.Stats(); stats.OpenIndexes != 1 || stats.Commits != 2 {
		t.Fatalf("host manager stats = %#v", stats)
	}
}

func TestCollectionSegmentCanaryIsAsyncAndSurvivesManagerRestart(t *testing.T) {
	root := t.TempDir()
	writeCollectionDocument(t, root, "posts", "kitwork.md", "Kitwork Search", "Một hệ thống của Nguyễn.")
	dataDirectory := filepath.Join(root, ".data")
	if err := os.MkdirAll(dataDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDirectory, "collection.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	defer db.Close()

	scope := newSegmentCanaryTestScope(t, root, db)
	scope.providerEntered = make(chan struct{})
	scope.providerRelease = make(chan struct{})
	var releaseProviderOnce sync.Once
	releaseProvider := func() {
		releaseProviderOnce.Do(func() { close(scope.providerRelease) })
	}
	defer releaseProvider()
	manager := NewManager(scope)
	manager.EnableSegmentSearchCanary(true)
	handle := openCollectionTestHandle(t, manager, "posts")

	result := make(chan value.Value, 1)
	go func() { result <- handle.Search(value.NewString("nguyen"), value.New(20)) }()
	var served value.Value
	select {
	case served = <-result:
		select {
		case <-scope.providerEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("shadow worker did not start")
		}
	case <-scope.providerEntered:
		select {
		case served = <-result:
		case <-time.After(5 * time.Second):
			t.Fatal("serving search remained blocked after the shadow provider entered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serving search and shadow worker did not make progress")
	}
	if served.K == value.Invalid {
		t.Fatalf("serving search: %v", served.V)
	}
	releaseProvider()
	waitForCollectionCanary(t, manager, 1)
	stats := manager.SegmentSearchCanaryStats()
	if !stats.Enabled || stats.Rebuilds != 1 || stats.Searches != 1 || stats.Matches != 1 ||
		stats.Mismatches != 0 || stats.Failures != 0 || stats.Dropped != 0 || stats.Canceled != 0 ||
		stats.Stale != 0 || stats.PendingTasks != 0 || stats.PeakPendingTasks != 1 {
		t.Fatalf("canary stats = %#v", stats)
	}
	manager.EnableSegmentSearchCanary(false)
	if result := handle.Search(value.NewString("nguyen")); result.K == value.Invalid {
		t.Fatalf("serving search after disabling canary: %v", result.V)
	}
	if after := manager.SegmentSearchCanaryStats(); after.Searches != stats.Searches || after.Enabled {
		t.Fatalf("disabled canary changed stats: before=%#v after=%#v", stats, after)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scope.searchManager.Close(); err != nil {
		t.Fatal(err)
	}

	reopenedSearch, err := searchengine.NewManager(
		scope.searchRoot, searchengine.ManagerOptions{DisableAutoCompact: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedSearch.Close()
	reopenedScope := &segmentCanaryTestScope{
		root: root, db: db, searchRoot: scope.searchRoot, searchManager: reopenedSearch,
		canary: true, keys: make(map[string]struct{}),
	}
	reopened := NewManager(reopenedScope)
	defer reopened.Close()
	reopenedHandle := openCollectionTestHandle(t, reopened, "posts")
	if result := reopenedHandle.Search(value.NewString("nguyen")); result.K == value.Invalid {
		t.Fatalf("reopened serving search: %v", result.V)
	}
	waitForCollectionCanary(t, reopened, 1)
	if stats := reopened.SegmentSearchCanaryStats(); stats.Rebuilds != 0 || stats.Matches != 1 || stats.Failures != 0 {
		t.Fatalf("reopened canary stats = %#v", stats)
	}
	if stats := reopenedSearch.Stats(); stats.OpenIndexes != 1 || stats.Commits != 0 {
		t.Fatalf("reopened host manager rebuilt projection: %#v", stats)
	}
}

func TestCollectionSegmentCanaryQueueIsBounded(t *testing.T) {
	root := t.TempDir()
	writeCollectionDocument(t, root, "posts", "kitwork.md", "Kitwork", "Nguyễn")
	scope := newSegmentCanaryTestScope(t, root, nil)
	scope.providerEntered = make(chan struct{})
	scope.providerRelease = make(chan struct{})
	manager := NewManager(scope)
	manager.EnableSegmentSearchCanary(true)
	handle := openCollectionTestHandle(t, manager, "posts")
	index, err := handle.collection.Index()
	if err != nil {
		t.Fatal(err)
	}
	signature := collectionDirSignature(index)
	handle.runSegmentSearchCanary(signature, "nguyen", 20, nil)
	select {
	case <-scope.providerEntered:
	case <-time.After(time.Second):
		close(scope.providerRelease)
		t.Fatal("shadow worker did not block in provider")
	}
	for attempt := 0; attempt < collectionSegmentCanaryQueue+10; attempt++ {
		handle.runSegmentSearchCanary(signature, "nguyen", 20, nil)
	}
	stats := manager.SegmentSearchCanaryStats()
	if stats.Searches != collectionSegmentCanaryQueue+1 || stats.Dropped != 10 ||
		stats.PendingTasks != collectionSegmentCanaryQueue+1 {
		close(scope.providerRelease)
		t.Fatalf("bounded canary queue stats = %#v", stats)
	}
	close(scope.providerRelease)
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	stats = manager.SegmentSearchCanaryStats()
	terminal := stats.Matches + stats.Mismatches + stats.Failures + stats.Canceled + stats.Stale
	if terminal != stats.Searches {
		t.Fatalf("closed canary left accepted work unresolved: %#v", stats)
	}
	if stats.PendingTasks != 0 {
		t.Fatalf("closed canary retained host admission: %#v", stats)
	}
}

func TestCollectionSegmentCanaryHostAdmissionIsBounded(t *testing.T) {
	telemetry := NewSegmentSearchCanaryTelemetry()
	for task := 0; task < collectionSegmentCanaryHostTasks; task++ {
		if !telemetry.acquireTask() {
			t.Fatalf("host admission rejected task %d", task)
		}
	}
	if telemetry.acquireTask() {
		t.Fatal("host admission exceeded its task bound")
	}
	if stats := telemetry.Snapshot(); stats.PendingTasks != collectionSegmentCanaryHostTasks ||
		stats.PeakPendingTasks != collectionSegmentCanaryHostTasks {
		t.Fatalf("bounded host admission stats = %#v", stats)
	}
	for task := 0; task < collectionSegmentCanaryHostTasks; task++ {
		telemetry.releaseTask()
	}
	if stats := telemetry.Snapshot(); stats.PendingTasks != 0 ||
		stats.PeakPendingTasks != collectionSegmentCanaryHostTasks {
		t.Fatalf("released host admission stats = %#v", stats)
	}
}

func writeCollectionDocument(t *testing.T, root, collection, name, title, body string) {
	t.Helper()
	directory := filepath.Join(root, "_collection", collection)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ntitle: " + title + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openCollectionTestHandle(t *testing.T, manager *Manager, name string) *Handle {
	t.Helper()
	opened := manager.Open(value.NewString(name))
	if opened.K == value.Invalid {
		t.Fatalf("open collection: %v", opened.V)
	}
	handle, ok := opened.V.(*Handle)
	if !ok {
		t.Fatalf("collection handle = %T", opened.V)
	}
	return handle
}

func waitForCollectionCanary(t *testing.T, manager *Manager, searches uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stats := manager.SegmentSearchCanaryStats()
		completed := stats.Matches + stats.Mismatches + stats.Failures + stats.Canceled + stats.Stale
		if stats.Searches >= searches && completed >= searches && stats.PendingTasks == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("canary did not drain: %#v", manager.SegmentSearchCanaryStats())
}
