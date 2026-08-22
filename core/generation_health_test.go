package core

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/kitwork/engine/work"
)

const invalidGenerationRouter = `import { router } from "kitwork";
router.get().handle((ctx) => {`

func TestEngineGenerationCandidateLifecycleOutcomes(t *testing.T) {
	t.Run("cold valid", func(t *testing.T) {
		root := t.TempDir()
		writeTreeTenant(t, root, "cold-valid")
		engine := New(root, 0, false, "")
		t.Cleanup(engine.Close)

		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		if response.Code != http.StatusOK || response.Body.String() != "cold-valid" {
			t.Fatalf("cold valid response = %d %q", response.Code, response.Body.String())
		}
		assertGenerationCandidateHealth(t, engine.Health(), 1, 0, 1, 0, 0)
	})

	t.Run("cold invalid", func(t *testing.T) {
		root := t.TempDir()
		routerFile := writeTreeTenant(t, root, "unused")
		if err := os.WriteFile(routerFile, []byte(invalidGenerationRouter), 0o644); err != nil {
			t.Fatal(err)
		}
		engine := New(root, 0, false, "")
		t.Cleanup(engine.Close)

		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("cold invalid response = %d %q", response.Code, response.Body.String())
		}
		snapshot := engine.Health()
		assertGenerationCandidateHealth(t, snapshot, 0, 1, 0, 0, 0)
		if snapshot.LoadedSites != 0 || snapshot.ActiveGenerations != 0 {
			t.Fatalf("invalid cold candidate retained ownership = %+v", snapshot)
		}
	})

	t.Run("hot valid", func(t *testing.T) {
		root := t.TempDir()
		routerFile := writeTreeTenant(t, root, "v1")
		engine := New(root, 0, true, "")
		t.Cleanup(engine.Close)
		serveGenerationHealthRequest(t, engine, "v1")

		engine.mu.RLock()
		cached := engine.cache["localhost"]
		engine.mu.RUnlock()
		writeRouterBody(t, routerFile, "v2")
		cached.mu.Lock()
		cached.lastChecked = time.Time{}
		cached.mu.Unlock()
		serveGenerationHealthRequest(t, engine, "v2")

		assertGenerationCandidateHealth(t, engine.Health(), 2, 0, 2, 0, 1)
	})

	t.Run("hot invalid", func(t *testing.T) {
		root := t.TempDir()
		routerFile := writeTreeTenant(t, root, "v1")
		engine := New(root, 0, true, "")
		t.Cleanup(engine.Close)
		serveGenerationHealthRequest(t, engine, "v1")

		engine.mu.RLock()
		cached := engine.cache["localhost"]
		engine.mu.RUnlock()
		current := cached.current()
		if err := os.WriteFile(routerFile, []byte(invalidGenerationRouter), 0o644); err != nil {
			t.Fatal(err)
		}
		cached.mu.Lock()
		cached.lastChecked = time.Time{}
		cached.mu.Unlock()
		serveGenerationHealthRequest(t, engine, "v1")

		if cached.current() != current {
			t.Fatal("invalid hot candidate replaced the current tenant")
		}
		assertGenerationCandidateHealth(t, engine.Health(), 1, 1, 1, 0, 0)
	})

	t.Run("activation invalid", func(t *testing.T) {
		root := t.TempDir()
		writeTreeTenant(t, root, "candidate")
		engine := New(root, 0, false, "")
		t.Cleanup(engine.Close)

		engine.mu.Lock()
		appRuntime := engine.appRuntimeLocked("", "localhost")
		engine.mu.Unlock()
		siteRuntime, err := appRuntime.Site(root, "localhost")
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := engine.prepareTenantCandidate("localhost", appRuntime, siteRuntime)
		if err != nil {
			t.Fatal(err)
		}
		siteRuntime.Close()
		if err := engine.activateGeneration(candidate); err == nil {
			t.Fatal("closed site runtime accepted candidate activation")
		}
		candidate.Close()
		appRuntime.RemoveSite("localhost")

		assertGenerationCandidateHealth(t, engine.Health(), 1, 0, 0, 1, 0)
	})
}

func TestPrepareTenantCandidateRegistersFinishBeforeCleanup(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "engine.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var candidate *ast.FuncDecl
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "prepareTenantCandidate" {
			candidate = function
			break
		}
	}
	if candidate == nil || candidate.Body == nil {
		t.Fatal("prepareTenantCandidate declaration not found")
	}
	var defers []*ast.DeferStmt
	for _, statement := range candidate.Body.List {
		if deferred, ok := statement.(*ast.DeferStmt); ok {
			defers = append(defers, deferred)
		}
	}
	if len(defers) < 2 ||
		!astNodeHasIdentifier(defers[0], "finish") ||
		!astNodeHasSelector(defers[1], "Close") ||
		!astNodeHasSelector(defers[1], "Retire") {
		t.Fatal("preparation finish must be registered before the cleanup defer")
	}
}

func astNodeHasIdentifier(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(current ast.Node) bool {
		if identifier, ok := current.(*ast.Ident); ok && identifier.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func astNodeHasSelector(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(current ast.Node) bool {
		if selector, ok := current.(*ast.SelectorExpr); ok && selector.Sel.Name == name {
			found = true
			return false
		}
		return !found
	})
	return found
}

func TestEngineHealthOwnershipSnapshotIsNonblocking(t *testing.T) {
	root := t.TempDir()
	writeTreeTenant(t, root, "healthy")
	engine := New(root, 0, false, "")
	t.Cleanup(engine.Close)
	serveGenerationHealthRequest(t, engine, "healthy")

	finishPrepare := engine.runtimeHealth.BeginGenerationPrepare()
	finishActivate := engine.runtimeHealth.BeginGenerationActivate()
	finished := false
	t.Cleanup(func() {
		if !finished {
			finishPrepare(false)
			finishActivate(false)
		}
	})

	engine.mu.Lock()
	result := make(chan work.RuntimeHealthSnapshot, 1)
	go func() {
		result <- engine.Health()
	}()
	var unavailable work.RuntimeHealthSnapshot
	select {
	case unavailable = <-result:
		engine.mu.Unlock()
	case <-time.After(500 * time.Millisecond):
		engine.mu.Unlock()
		t.Fatal("Engine.Health blocked on the engine ownership lock")
	}
	finishPrepare(true)
	finishActivate(false)
	finished = true

	if unavailable.OwnershipSnapshotAvailable ||
		unavailable.LoadedApps != 0 ||
		unavailable.LoadedSites != 0 ||
		unavailable.ActiveGenerations != 0 ||
		unavailable.ActiveGenerationLeases != 0 ||
		unavailable.Generations.Preparing != 1 ||
		unavailable.Generations.Activating != 1 {
		t.Fatalf("unavailable ownership snapshot = %+v", unavailable)
	}
	payload, err := json.Marshal(unavailable)
	if err != nil {
		t.Fatal(err)
	}
	var decodedHealth work.RuntimeHealthSnapshot
	if err := json.Unmarshal(payload, &decodedHealth); err != nil {
		t.Fatal(err)
	}
	if decodedHealth.OwnershipSnapshotAvailable {
		t.Fatalf("serialized unavailable ownership snapshot = %s", payload)
	}

	available := engine.Health()
	if !available.OwnershipSnapshotAvailable ||
		available.LoadedSites != 1 ||
		available.ActiveGenerations != 1 ||
		available.Generations.Preparing != 0 ||
		available.Generations.Activating != 0 ||
		available.Generations.Prepared != 2 ||
		available.Generations.ActivateFailures != 1 {
		t.Fatalf("available ownership snapshot = %+v", available)
	}

	diagnosticPayload, err := json.Marshal(engine.Diagnostics())
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics DiagnosticSnapshot
	if err := json.Unmarshal(diagnosticPayload, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if !diagnostics.Engine.PolicySnapshotAvailable ||
		!diagnostics.Health.OwnershipSnapshotAvailable {
		t.Fatalf("serialized diagnostics omitted available ownership: %s", diagnosticPayload)
	}
}

func TestEngineHealthObservesActualCandidatePreparation(t *testing.T) {
	root := t.TempDir()
	writeTreeTenant(t, root, "prepared")
	engine := New(root, 0, false, "")
	t.Cleanup(engine.Close)

	engine.bytecodeCacheMu.Lock()
	cacheLocked := true
	t.Cleanup(func() {
		if cacheLocked {
			engine.bytecodeCacheMu.Unlock()
		}
	})
	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
		requestDone <- response
	}()

	var preparing work.RuntimeHealthSnapshot
	waitGenerationHealth(t, "actual candidate preparation gauge", func() bool {
		preparing = engine.Health()
		return preparing.Generations.Preparing == 1
	})
	if preparing.OwnershipSnapshotAvailable ||
		preparing.LoadedApps != 0 ||
		preparing.LoadedSites != 0 ||
		preparing.ActiveGenerations != 0 ||
		preparing.Generations.Activating != 0 {
		t.Fatalf("blocked actual preparation snapshot = %+v", preparing)
	}

	engine.bytecodeCacheMu.Unlock()
	cacheLocked = false
	select {
	case response := <-requestDone:
		if response.Code != http.StatusOK || response.Body.String() != "prepared" {
			t.Fatalf("prepared response = %d %q", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("candidate preparation did not resume")
	}

	completed := engine.Health()
	if !completed.OwnershipSnapshotAvailable ||
		completed.Generations.Preparing != 0 ||
		completed.Generations.Prepared != 1 ||
		completed.Generations.Activated != 1 ||
		completed.ActiveGenerations != 1 {
		t.Fatalf("completed actual preparation snapshot = %+v", completed)
	}
}

func TestEngineHealthAvailableSnapshotDoesNotCrossActivationWriter(t *testing.T) {
	engine := New(t.TempDir(), 0, false, "")
	t.Cleanup(engine.Close)

	const cycles = 32
	const readers = 8
	for cycle := 0; cycle < cycles; cycle++ {
		engine.mu.Lock()
		finishActivate := engine.runtimeHealth.BeginGenerationActivate()
		results := make(chan work.RuntimeHealthSnapshot, readers)
		start := make(chan struct{})
		for reader := 0; reader < readers; reader++ {
			go func() {
				<-start
				results <- engine.Health()
			}()
		}
		close(start)
		runtime.Gosched()
		finishActivate(true)
		engine.mu.Unlock()

		for reader := 0; reader < readers; reader++ {
			snapshot := <-results
			if snapshot.OwnershipSnapshotAvailable && snapshot.Generations.Activating != 0 {
				t.Fatalf(
					"available snapshot crossed activation writer: %+v",
					snapshot.Generations,
				)
			}
		}
	}
}

func serveGenerationHealthRequest(t *testing.T, engine *Engine, body string) {
	t.Helper()
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if response.Code != http.StatusOK || response.Body.String() != body {
		t.Fatalf("generation response = %d %q, want 200 %q", response.Code, response.Body.String(), body)
	}
}

func assertGenerationCandidateHealth(
	t *testing.T,
	snapshot work.RuntimeHealthSnapshot,
	prepared uint64,
	prepareFailures uint64,
	activated uint64,
	activateFailures uint64,
	drainAttemptsSucceeded uint64,
) {
	t.Helper()
	if !snapshot.OwnershipSnapshotAvailable ||
		snapshot.Generations.Prepared != prepared ||
		snapshot.Generations.PrepareFailures != prepareFailures ||
		snapshot.Generations.Activated != activated ||
		snapshot.Generations.ActivateFailures != activateFailures ||
		snapshot.Generations.Drained != drainAttemptsSucceeded ||
		snapshot.Generations.DrainFailures != 0 ||
		snapshot.Generations.Preparing != 0 ||
		snapshot.Generations.Activating != 0 ||
		snapshot.Generations.Draining != 0 ||
		snapshot.Latencies.GenerationPrepare.Count != prepared+prepareFailures ||
		snapshot.Latencies.GenerationActivate.Count != activated+activateFailures {
		t.Fatalf("generation candidate health = %+v latencies=%+v", snapshot.Generations, snapshot.Latencies)
	}
}

func TestEngineHealthTracksRetiredGenerationDrain(t *testing.T) {
	root := t.TempDir()
	routerFile := writeTreeTenant(t, root, "v1")
	engine := New(root, 0, true, "")
	t.Cleanup(engine.Close)

	initial := httptest.NewRecorder()
	engine.ServeHTTP(
		initial,
		httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
	)
	if initial.Code != http.StatusOK {
		t.Fatalf("initial response = %d %q", initial.Code, initial.Body.String())
	}

	engine.mu.RLock()
	cached := engine.cache["localhost"]
	engine.mu.RUnlock()
	if cached == nil {
		t.Fatal("initial generation was not cached")
	}
	oldTenant := cached.current()
	oldGeneration := oldTenant.SiteGeneration()
	lease, ok := oldGeneration.Acquire()
	if !ok {
		t.Fatal("could not hold old generation lease")
	}
	leaseReleased := false
	t.Cleanup(func() {
		if !leaseReleased {
			lease.Release()
		}
	})

	baseline := engine.Health()
	writeRouterBody(t, routerFile, "v2")
	cached.mu.Lock()
	cached.lastChecked = time.Time{}
	cached.mu.Unlock()

	reloadDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		engine.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
		)
		reloadDone <- response
	}()

	waitGenerationHealth(t, "retired generation drain", func() bool {
		snapshot := engine.Health()
		return cached.current() != oldTenant &&
			oldGeneration.Retired() &&
			snapshot.Generations.Draining == 1 &&
			snapshot.Generations.DrainingLeases == 1 &&
			snapshot.Generations.OldestDrainNanoseconds > 0
	})
	draining := engine.Health()
	if draining.ActiveGenerations != 1 ||
		draining.ActiveGenerationLeases != 1 ||
		draining.Generations.Drained != baseline.Generations.Drained ||
		draining.Latencies.GenerationDrain.Count != baseline.Latencies.GenerationDrain.Count {
		t.Fatalf("live drain snapshot = %+v", draining)
	}
	select {
	case response := <-reloadDone:
		t.Fatalf("reload completed before held lease released: %d %q", response.Code, response.Body.String())
	default:
	}

	lease.Release()
	leaseReleased = true
	select {
	case response := <-reloadDone:
		if response.Code != http.StatusOK || response.Body.String() != "v2" {
			t.Fatalf("reload response = %d %q", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reload did not complete after held lease released")
	}

	drained := engine.Health()
	if drained.Generations.Draining != 0 ||
		drained.Generations.DrainingLeases != 0 ||
		drained.Generations.OldestDrainNanoseconds != 0 ||
		drained.ActiveGenerationLeases != 0 ||
		drained.Generations.Drained != baseline.Generations.Drained+1 ||
		drained.Latencies.GenerationDrain.Count != baseline.Latencies.GenerationDrain.Count+1 ||
		drained.Generations.MaxOldestDrainNanoseconds == 0 {
		t.Fatalf("completed drain snapshot = %+v", drained)
	}

	engine.Close()
	closed := engine.Health()
	if closed.LoadedSites != 0 ||
		closed.ActiveGenerations != 0 ||
		closed.ActiveGenerationLeases != 0 ||
		closed.Generations.Draining != 0 ||
		closed.Generations.DrainingLeases != 0 ||
		closed.Generations.Drained != baseline.Generations.Drained+2 ||
		closed.Latencies.GenerationDrain.Count != baseline.Latencies.GenerationDrain.Count+2 {
		t.Fatalf("closed engine health = %+v", closed)
	}
}

func waitGenerationHealth(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
