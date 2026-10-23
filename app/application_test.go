package app_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/value"
)

type capabilityCloser struct {
	closed atomic.Int32
}

type testConnector struct{}

func (testConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("not used")
}
func (testConnector) Driver() driver.Driver { return testDriver{} }

type testDriver struct{}

func (testDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("not used")
}

func (c *capabilityCloser) Close() error {
	c.closed.Add(1)
	return nil
}

func TestRuntimeSharesAppAndIsolatesSites(t *testing.T) {
	runtime := app.NewRuntime("identity-a")
	first, err := runtime.Site("apps", "a.example")
	if err != nil {
		t.Fatal(err)
	}
	firstAgain, err := runtime.Site("apps", "a.example")
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Site("apps", "b.example")
	if err != nil {
		t.Fatal(err)
	}

	if first != firstAgain {
		t.Fatal("one domain produced more than one site runtime")
	}
	if first == second {
		t.Fatal("different domains shared a site runtime")
	}
	if first.App() != runtime || second.App() != runtime {
		t.Fatal("site runtime does not reference its owning app runtime")
	}
	if runtime.SiteCount() != 2 {
		t.Fatalf("expected two sites, got %d", runtime.SiteCount())
	}

	runtime.RemoveSite("a.example")
	if !first.Closed() {
		t.Fatal("removed site was not closed")
	}
	if second.Closed() {
		t.Fatal("removing one site closed its sibling")
	}

	runtime.Close()
	if !runtime.Closed() || !second.Closed() {
		t.Fatal("app close did not close the remaining site hierarchy")
	}
}

func TestRuntimeSiteIsConcurrentSingleton(t *testing.T) {
	runtime := app.NewRuntime("identity-a")
	const workers = 64
	sites := make([]any, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(index int) {
			defer wg.Done()
			current, err := runtime.Site("apps", "same.example")
			if err != nil {
				t.Errorf("site %d: %v", index, err)
				return
			}
			sites[index] = current
		}(i)
	}
	wg.Wait()

	for i := 1; i < len(sites); i++ {
		if sites[i] != sites[0] {
			t.Fatal("concurrent calls created more than one site runtime")
		}
	}
	if runtime.SiteCount() != 1 {
		t.Fatalf("expected one site runtime, got %d", runtime.SiteCount())
	}
}

func TestRuntimeClosesAppCapabilities(t *testing.T) {
	runtime := app.NewRuntime("identity-a")
	registry := capabilities.NewRegistry()
	closer := &capabilityCloser{}
	registry.RegisterWithLifetime("resource", capabilities.LifetimeApp, func(capabilities.Scope) value.Value {
		return value.New(closer)
	})

	if _, ok := runtime.CapabilitiesCache().GetOrCompute("resource", registry, nil); !ok {
		t.Fatal("app capability was not created")
	}
	runtime.Close()
	runtime.Close()
	if got := closer.closed.Load(); got != 1 {
		t.Fatalf("app capability closed %d times, want 1", got)
	}
	if _, ok := runtime.CapabilitiesCache().GetOrCompute("resource", registry, nil); ok {
		t.Fatal("closed app runtime recreated an app capability")
	}
}

func TestRuntimeOwnsSharedDatabaseManager(t *testing.T) {
	runtime := app.NewRuntime("identity-a")
	const workers = 32
	connections := make([]*sql.DB, workers)
	var opens atomic.Int32

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(index int) {
			defer wg.Done()
			connection, err := runtime.Databases().Open("shared", func() (*sql.DB, error) {
				opens.Add(1)
				return sql.OpenDB(testConnector{}), nil
			})
			if err != nil {
				t.Errorf("open %d: %v", index, err)
				return
			}
			connections[index] = connection
		}(i)
	}
	wg.Wait()

	for i := 1; i < len(connections); i++ {
		if connections[i] != connections[0] {
			t.Fatal("one app opened more than one connection for a shared key")
		}
	}
	if opens.Load() != 1 || runtime.Databases().Count() != 1 {
		t.Fatalf("opens=%d connections=%d, want 1/1", opens.Load(), runtime.Databases().Count())
	}
	runtime.Close()
	if _, err := runtime.Databases().Open("late", func() (*sql.DB, error) {
		return sql.OpenDB(testConnector{}), nil
	}); err == nil {
		t.Fatal("closed app database manager accepted a connection")
	}
}

type orderedResource struct {
	taskFinished *atomic.Bool
	closed       atomic.Bool
}

func (r *orderedResource) Close() {
	if !r.taskFinished.Load() {
		panic("app resource closed before accepted tasks drained")
	}
	r.closed.Store(true)
}

func TestRuntimeDrainsTasksBeforeResources(t *testing.T) {
	runtime := app.NewRuntime("identity-a")
	taskContext, done, ok := runtime.Tasks().Start()
	if !ok {
		t.Fatal("open app rejected a task")
	}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var taskFinished atomic.Bool
	go func() {
		defer done()
		close(started)
		<-taskContext.Done()
		close(cancelled)
		<-release
		taskFinished.Store(true)
	}()
	<-started

	resource := &orderedResource{taskFinished: &taskFinished}
	if _, installed, err := runtime.InstallResource("scheduler", resource); err != nil || !installed {
		t.Fatalf("install resource: installed=%v err=%v", installed, err)
	}

	closed := make(chan struct{})
	go func() {
		runtime.Close()
		close(closed)
	}()
	<-cancelled
	select {
	case <-closed:
		t.Fatal("app closed before an accepted task drained")
	default:
	}
	close(release)
	<-closed
	if !resource.closed.Load() {
		t.Fatal("app resource was not closed after task drain")
	}
	if _, _, ok := runtime.Tasks().Start(); ok {
		t.Fatal("closed app accepted another task")
	}
}

// closingResource is a resource that says when it closed.
type closingResource struct {
	closed atomic.Bool
}

func (r *closingResource) Close() { r.closed.Store(true) }

// drainingResource is a worker: its Drain is the work that ends while the app closes, which still needs
// the other resource (the app's database) installed and open to record what it did.
type drainingResource struct {
	runtime *app.Runtime
	other   *closingResource
	// saw is what the drain found of the other resource: "" when it was there and open.
	saw     string
	drained atomic.Int32
	closed  atomic.Bool
}

func (r *drainingResource) Drain() {
	r.drained.Add(1)
	switch current, _ := r.runtime.Resource("database").(*closingResource); {
	case current == nil:
		r.saw = "the other resource was no longer installed"
	case current.closed.Load():
		r.saw = "the other resource was closed"
	}
}

func (r *drainingResource) Close() {
	if r.drained.Load() == 0 {
		panic("a drainer closed before it drained")
	}
	r.closed.Store(true)
}

// A resource that drains (a queue worker) is drained before any resource closes, every resource still
// installed: work that ends during the drain records its outcome through the others. Several installs in
// either order, so the map's random order cannot pass the test by luck. CONTROL: a resource that does not
// drain is only closed.
func TestRuntimeDrainsWorkersBeforeClosingResources(t *testing.T) {
	for round := 0; round < 20; round++ {
		runtime := app.NewRuntime("identity-a")
		database := &closingResource{}
		worker := &drainingResource{runtime: runtime, other: database}
		first, second := app.Resource(worker), app.Resource(database)
		firstName, secondName := "queue", "database"
		if round%2 == 1 {
			first, second, firstName, secondName = second, first, secondName, firstName
		}
		if _, installed, err := runtime.InstallResource(firstName, first); err != nil || !installed {
			t.Fatalf("install %s: installed=%v err=%v", firstName, installed, err)
		}
		if _, installed, err := runtime.InstallResource(secondName, second); err != nil || !installed {
			t.Fatalf("install %s: installed=%v err=%v", secondName, installed, err)
		}
		runtime.Close()
		if worker.drained.Load() != 1 || worker.saw != "" {
			t.Fatalf("round %d: drained %d times; %s", round, worker.drained.Load(), worker.saw)
		}
		if !worker.closed.Load() || !database.closed.Load() {
			t.Fatalf("round %d: closed worker=%v database=%v, want both", round, worker.closed.Load(), database.closed.Load())
		}
	}
}
