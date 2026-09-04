package work

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

const (
	kitDBNodeCatalogFaultChildEnvironment = "KITDB_NODE_CATALOG_FAULT_CHILD"
	kitDBNodeCatalogFaultModeEnvironment  = "KITDB_NODE_CATALOG_FAULT_MODE"
	kitDBNodeCatalogFaultStageEnvironment = "KITDB_NODE_CATALOG_FAULT_STAGE"
	kitDBNodeCatalogFaultRootEnvironment  = "KITDB_NODE_CATALOG_FAULT_ROOT"
	kitDBNodeCatalogFaultExitCode         = 87

	kitDBNodeCatalogFaultCreateAfterIntent   = "create-after-intent"
	kitDBNodeCatalogFaultCreateAfterTarget   = "create-after-target"
	kitDBNodeCatalogFaultCreateAfterActive   = "create-after-active"
	kitDBNodeCatalogFaultDropAfterHide       = "drop-after-hide"
	kitDBNodeCatalogFaultDropAfterDropping   = "drop-after-dropping"
	kitDBNodeCatalogFaultDropAfterStorage    = "drop-after-storage"
	kitDBNodeCatalogFaultDropAfterCatalog    = "drop-after-catalog"
	kitDBNodeCatalogFaultRenameBeforeCatalog = "rename-before-catalog"
	kitDBNodeCatalogFaultRenameAfterCatalog  = "rename-after-catalog"

	kitDBNodeCatalogFaultSourceName  = "source.kitdb"
	kitDBNodeCatalogFaultTargetName  = "fault_target"
	kitDBNodeCatalogFaultTargetStore = kitDBNodeCatalogFaultTargetName + ".kitdb"
	kitDBNodeCatalogFaultRenamedName = "fault_renamed"
	kitDBNodeCatalogFaultToken       = "node-catalog-fault-secret"
	kitDBNodeCatalogFaultModeFork    = "fork"
	kitDBNodeCatalogFaultModeEmpty   = "empty"
)

var kitDBNodeCatalogFaultStages = []string{
	kitDBNodeCatalogFaultCreateAfterIntent,
	kitDBNodeCatalogFaultCreateAfterTarget,
	kitDBNodeCatalogFaultCreateAfterActive,
	kitDBNodeCatalogFaultDropAfterHide,
	kitDBNodeCatalogFaultDropAfterDropping,
	kitDBNodeCatalogFaultDropAfterStorage,
	kitDBNodeCatalogFaultDropAfterCatalog,
}

type kitDBNodeCatalogFaultTopology struct {
	root       string
	data       string
	sourcePath string
	targetPath string
	sourceID   string
}

func TestKitDBNodeCatalogHardCrashMatrix(t *testing.T) {
	for _, stage := range kitDBNodeCatalogFaultStages {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			topology := prepareKitDBNodeCatalogFaultTopology(
				t,
				t.TempDir(),
				!kitDBNodeCatalogFaultCreateStage(stage),
			)
			runKitDBNodeCatalogFaultChild(t, topology, stage, kitDBNodeCatalogFaultModeFork)
			recoverKitDBNodeCatalogFaultTopology(t, topology, stage, true)
		})
	}
}

func TestKitDBNodeCatalogEmptyCreateHardCrashMatrix(t *testing.T) {
	for _, stage := range []string{
		kitDBNodeCatalogFaultCreateAfterIntent,
		kitDBNodeCatalogFaultCreateAfterTarget,
		kitDBNodeCatalogFaultCreateAfterActive,
	} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			topology := prepareKitDBNodeCatalogFaultTopology(t, t.TempDir(), false)
			runKitDBNodeCatalogFaultChild(t, topology, stage, kitDBNodeCatalogFaultModeEmpty)
			recoverKitDBNodeCatalogFaultTopology(t, topology, stage, false)
		})
	}
}

func TestKitDBNodeCatalogRenameHardCrashMatrix(t *testing.T) {
	for _, stage := range []string{
		kitDBNodeCatalogFaultRenameBeforeCatalog,
		kitDBNodeCatalogFaultRenameAfterCatalog,
	} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			topology := prepareKitDBNodeCatalogFaultTopology(t, t.TempDir(), true)
			runKitDBNodeCatalogFaultChild(t, topology, stage, kitDBNodeCatalogFaultModeFork)
			recoverKitDBNodeCatalogRenameFaultTopology(t, topology, stage)
		})
	}
}

// TestKitDBNodeCatalogCrashChild is invoked only as a subprocess. A successful
// child deliberately exits while the tenant, node manager, catalog mutex, and
// database handles are still live, so no deferred cleanup can manufacture the
// recovery result.
func TestKitDBNodeCatalogCrashChild(t *testing.T) {
	if os.Getenv(kitDBNodeCatalogFaultChildEnvironment) != "1" {
		return
	}
	root := os.Getenv(kitDBNodeCatalogFaultRootEnvironment)
	stage := os.Getenv(kitDBNodeCatalogFaultStageEnvironment)
	mode := os.Getenv(kitDBNodeCatalogFaultModeEnvironment)
	if root == "" || !kitDBNodeCatalogFaultKnownStage(stage) ||
		(mode != kitDBNodeCatalogFaultModeFork && mode != kitDBNodeCatalogFaultModeEmpty) {
		t.Fatalf(
			"node catalog fault child input is incomplete: root=%q stage=%q mode=%q",
			root,
			stage,
			mode,
		)
	}
	tenant := NewTenant(root, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	authenticator := &kitDBPostgresAuthenticator{tenant: tenant}
	if kitDBNodeCatalogFaultCreateStage(stage) {
		runKitDBNodeCatalogCreateFaultChild(t, tenant, stage, mode)
	}
	if mode != kitDBNodeCatalogFaultModeFork {
		t.Fatalf("node catalog drop fault requires fork mode, got %q", mode)
	}
	if err := authenticator.restoreKitDBNodeCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if kitDBNodeCatalogFaultRenameStage(stage) {
		runKitDBNodeCatalogRenameFaultChild(t, tenant, stage)
	}
	runKitDBNodeCatalogDropFaultChild(t, tenant, stage)
}

func prepareKitDBNodeCatalogFaultTopology(
	t *testing.T,
	root string,
	activeTarget bool,
) kitDBNodeCatalogFaultTopology {
	t.Helper()
	directory := filepath.Join(root, "test", "localhost")
	data := filepath.Join(directory, ".data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	router := `import { router, database } from "kitwork";
const { kitdb } = database;
kitdb("source.kitdb", {}, {
  token: "node-catalog-fault-secret",
  access: "readwrite",
  recovery: true,
});
router.get(() => "ok");`
	if err := os.WriteFile(filepath.Join(directory, RouterFileName), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	topology := kitDBNodeCatalogFaultTopology{
		root:       root,
		data:       data,
		sourcePath: filepath.Join(data, kitDBNodeCatalogFaultSourceName),
		targetPath: filepath.Join(data, kitDBNodeCatalogFaultTargetStore),
	}
	source, err := kitdbengine.OpenWithOptions(
		topology.sourcePath,
		kitdbengine.OpenOptions{RetainHistory: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	topology.sourceID = source.ID()
	transaction, err := source.Begin()
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("fault/sentinel"), []byte("durable")); err != nil {
		_ = transaction.Rollback()
		_ = source.Close()
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if !activeTarget {
		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
		return topology
	}
	result, err := source.ForkToTime(
		context.Background(),
		topology.targetPath,
		time.Now().UTC().Add(time.Second),
	)
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	entry := newKitDBNodeCatalogEntry(
		kitDBNodeCatalogFaultTargetName,
		kitDBNodeCatalogFaultSourceName,
	)
	entry.State = kitDBNodeCatalogActive
	entry.DatabaseID = result.DatabaseID
	catalog, err := kitdbengine.Open(filepath.Join(data, kitDBNodeCatalogFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := putKitDBNodeCatalogEntry(context.Background(), catalog, entry); err != nil {
		_ = catalog.Close()
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	return topology
}

func runKitDBNodeCatalogCreateFaultChild(
	t *testing.T,
	tenant *Tenant,
	stage string,
	mode string,
) {
	t.Helper()
	ctx := context.Background()
	manager, err := kitDBManagerForTenant(tenant)
	if err != nil {
		t.Fatal(err)
	}
	manager.catalogMu.Lock()
	entry := newKitDBNodeCatalogEntry(
		kitDBNodeCatalogFaultTargetName,
		kitDBNodeCatalogFaultSourceName,
	)
	if err := persistKitDBNodeCatalogEntry(ctx, tenant, manager, entry); err != nil {
		t.Fatal(err)
	}
	if stage == kitDBNodeCatalogFaultCreateAfterIntent {
		os.Exit(kitDBNodeCatalogFaultExitCode)
	}

	targetPath := tenant.resolve(".data", kitDBNodeCatalogFaultTargetStore)
	databaseID := ""
	switch mode {
	case kitDBNodeCatalogFaultModeFork:
		source, err := kitDBForRequest(tenant, kitDBNodeCatalogFaultSourceName, nil).database()
		if err != nil {
			t.Fatal(err)
		}
		result, forkErr := source.database.ForkToTime(
			ctx,
			targetPath,
			time.Now().UTC().Add(time.Second),
		)
		source.Release()
		if forkErr != nil {
			t.Fatal(forkErr)
		}
		databaseID = result.DatabaseID
	case kitDBNodeCatalogFaultModeEmpty:
		databaseID, err = createEmptyKitDBPostgresDatabase(ctx, manager, targetPath)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown create fault mode %q", mode)
	}
	if stage == kitDBNodeCatalogFaultCreateAfterTarget {
		os.Exit(kitDBNodeCatalogFaultExitCode)
	}

	target := &dbProxy{
		tenant: tenant, engine: "kitdb", dbName: kitDBNodeCatalogFaultTargetStore,
		databaseID: databaseID,
		tables:     map[string]map[string]*ColumnSpec{},
		structs:    map[string]*StructDef{},
	}
	registerSchema(target)
	if err := target.ensureKitDBCatalogLoaded(); err != nil {
		t.Fatal(err)
	}
	entry.State = kitDBNodeCatalogActive
	entry.DatabaseID = databaseID
	if err := persistKitDBNodeCatalogEntry(ctx, tenant, manager, entry); err != nil {
		t.Fatal(err)
	}
	if stage != kitDBNodeCatalogFaultCreateAfterActive {
		t.Fatalf("unknown create fault stage %q", stage)
	}
	os.Exit(kitDBNodeCatalogFaultExitCode)
}

func runKitDBNodeCatalogDropFaultChild(t *testing.T, tenant *Tenant, stage string) {
	t.Helper()
	ctx := context.Background()
	manager, err := kitDBManagerForTenant(tenant)
	if err != nil {
		t.Fatal(err)
	}
	manager.catalogMu.Lock()
	entries, exists, err := readKitDBNodeCatalog(ctx, tenant, manager)
	if err != nil || !exists {
		t.Fatalf("read drop fault catalog: exists=%t err=%v", exists, err)
	}
	entry, found := entries[kitDBNodeCatalogFaultTargetName]
	if !found || entry.State != kitDBNodeCatalogActive {
		t.Fatalf("drop fault entry = %#v", entry)
	}
	_, config, found := resolveServe(tenant, kitDBNodeCatalogFaultTargetStore)
	if !found || config.database == nil {
		t.Fatal("drop fault target is not exposed")
	}
	if _, registered, err := beginDropServe(
		tenant,
		kitDBNodeCatalogFaultTargetStore,
		config.database,
	); err != nil || !registered {
		t.Fatalf("hide drop fault target: registered=%t err=%v", registered, err)
	}
	if stage == kitDBNodeCatalogFaultDropAfterHide {
		os.Exit(kitDBNodeCatalogFaultExitCode)
	}

	entry.State = kitDBNodeCatalogDropping
	if err := persistKitDBNodeCatalogEntry(ctx, tenant, manager, entry); err != nil {
		t.Fatal(err)
	}
	if stage == kitDBNodeCatalogFaultDropAfterDropping {
		os.Exit(kitDBNodeCatalogFaultExitCode)
	}
	targetPath := tenant.resolve(".data", kitDBNodeCatalogFaultTargetStore)
	if err := manager.drop(ctx, targetPath); err != nil {
		t.Fatal(err)
	}
	if stage == kitDBNodeCatalogFaultDropAfterStorage {
		os.Exit(kitDBNodeCatalogFaultExitCode)
	}
	if err := removeKitDBNodeCatalogEntry(
		ctx,
		tenant,
		manager,
		kitDBNodeCatalogFaultTargetName,
	); err != nil {
		t.Fatal(err)
	}
	if stage != kitDBNodeCatalogFaultDropAfterCatalog {
		t.Fatalf("unknown drop fault stage %q", stage)
	}
	os.Exit(kitDBNodeCatalogFaultExitCode)
}

func runKitDBNodeCatalogRenameFaultChild(t *testing.T, tenant *Tenant, stage string) {
	t.Helper()
	ctx := context.Background()
	manager, err := kitDBManagerForTenant(tenant)
	if err != nil {
		t.Fatal(err)
	}
	manager.catalogMu.Lock()
	entries, exists, err := readKitDBNodeCatalog(ctx, tenant, manager)
	if err != nil || !exists {
		t.Fatalf("read rename fault catalog: exists=%t err=%v", exists, err)
	}
	entry, found := entries[kitDBNodeCatalogFaultTargetName]
	if !found || entry.State != kitDBNodeCatalogActive {
		t.Fatalf("rename fault entry = %#v", entry)
	}
	if stage == kitDBNodeCatalogFaultRenameBeforeCatalog {
		os.Exit(kitDBNodeCatalogFaultExitCode)
	}
	entry.Name = kitDBNodeCatalogFaultRenamedName
	if err := renamePersistedKitDBNodeCatalogEntry(
		ctx,
		tenant,
		manager,
		kitDBNodeCatalogFaultTargetName,
		entry,
	); err != nil {
		t.Fatal(err)
	}
	if stage != kitDBNodeCatalogFaultRenameAfterCatalog {
		t.Fatalf("unknown rename fault stage %q", stage)
	}
	os.Exit(kitDBNodeCatalogFaultExitCode)
}

func runKitDBNodeCatalogFaultChild(
	t *testing.T,
	topology kitDBNodeCatalogFaultTopology,
	stage string,
	mode string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestKitDBNodeCatalogCrashChild$",
		"-test.count=1",
		"-test.timeout=20s",
	)
	command.Env = append(
		os.Environ(),
		kitDBNodeCatalogFaultChildEnvironment+"=1",
		kitDBNodeCatalogFaultModeEnvironment+"="+mode,
		kitDBNodeCatalogFaultStageEnvironment+"="+stage,
		kitDBNodeCatalogFaultRootEnvironment+"="+topology.root,
	)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("node catalog fault child %q timed out: %v\n%s", stage, ctx.Err(), output)
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != kitDBNodeCatalogFaultExitCode {
		exitCode := 0
		if exitError != nil {
			exitCode = exitError.ExitCode()
		}
		t.Fatalf(
			"node catalog fault child %q = %v (exit=%d), want exit %d\n%s",
			stage,
			err,
			exitCode,
			kitDBNodeCatalogFaultExitCode,
			output,
		)
	}
}

func recoverKitDBNodeCatalogFaultTopology(
	t *testing.T,
	topology kitDBNodeCatalogFaultTopology,
	stage string,
	expectTargetSentinel bool,
) {
	t.Helper()
	tenant := NewTenant(topology.root, "localhost")
	if err := tenant.Run(); err != nil {
		tenant.Close()
		t.Fatal(err)
	}
	defer tenant.Close()
	authenticator := &kitDBPostgresAuthenticator{tenant: tenant}
	if err := authenticator.restoreKitDBNodeCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager, err := kitDBManagerForTenant(tenant)
	if err != nil {
		t.Fatal(err)
	}
	manager.catalogMu.Lock()
	entries, exists, err := readKitDBNodeCatalog(context.Background(), tenant, manager)
	manager.catalogMu.Unlock()
	if err != nil || !exists {
		t.Fatalf("recovered node catalog: exists=%t err=%v", exists, err)
	}
	catalog, _, err := openKitDBNodeCatalog(context.Background(), tenant, manager, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.database.Verify(); err != nil {
		catalog.Release()
		t.Fatalf("verify recovered node catalog: %v", err)
	}
	catalog.Release()

	expectActive := kitDBNodeCatalogFaultExpectsActive(stage)
	if expectActive {
		assertKitDBNodeCatalogFaultActive(
			t,
			tenant,
			entries,
			topology,
			expectTargetSentinel,
		)
	} else {
		if len(entries) != 0 {
			t.Fatalf("recovered removed entries = %#v", entries)
		}
		if _, _, found := resolveServe(tenant, kitDBNodeCatalogFaultTargetStore); found {
			t.Fatal("removed fault target was re-exposed")
		}
		assertKitDBNodeCatalogFaultStorageMissing(t, topology.targetPath)
	}

	source, err := kitDBForRequest(tenant, kitDBNodeCatalogFaultSourceName, nil).database()
	if err != nil {
		t.Fatal(err)
	}
	sourceID := source.database.ID()
	if sourceID != topology.sourceID {
		source.Release()
		t.Fatalf("source identity = %q, want %q", sourceID, topology.sourceID)
	}
	if err := source.database.Verify(); err != nil {
		source.Release()
		t.Fatalf("verify source after catalog recovery: %v", err)
	}
	value, found, err := source.database.Get([]byte("fault/sentinel"))
	source.Release()
	if err != nil || !found || string(value) != "durable" {
		t.Fatalf("source sentinel = %q found=%t err=%v", value, found, err)
	}
	if stats := manager.fleet.Stats(); stats.ActiveLeases != 0 {
		t.Fatalf("node catalog recovery retained %d active lease(s)", stats.ActiveLeases)
	}
}

func recoverKitDBNodeCatalogRenameFaultTopology(
	t *testing.T,
	topology kitDBNodeCatalogFaultTopology,
	stage string,
) {
	t.Helper()
	tenant := NewTenant(topology.root, "localhost")
	if err := tenant.Run(); err != nil {
		tenant.Close()
		t.Fatal(err)
	}
	defer tenant.Close()
	authenticator := &kitDBPostgresAuthenticator{tenant: tenant}
	if err := authenticator.restoreKitDBNodeCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager, err := kitDBManagerForTenant(tenant)
	if err != nil {
		t.Fatal(err)
	}
	manager.catalogMu.Lock()
	entries, exists, err := readKitDBNodeCatalog(context.Background(), tenant, manager)
	manager.catalogMu.Unlock()
	if err != nil || !exists || len(entries) != 1 {
		t.Fatalf("recovered rename catalog: exists=%t entries=%#v err=%v", exists, entries, err)
	}
	wantName := kitDBNodeCatalogFaultTargetName
	if stage == kitDBNodeCatalogFaultRenameAfterCatalog {
		wantName = kitDBNodeCatalogFaultRenamedName
	}
	entry, found := entries[wantName]
	if !found || entry.State != kitDBNodeCatalogActive ||
		entry.StorageName != kitDBNodeCatalogFaultTargetStore || entry.DatabaseID == "" {
		t.Fatalf("recovered rename entry = %#v", entry)
	}
	_, config, served := resolveServe(tenant, kitDBNodeCatalogFaultTargetStore)
	if !served || config.database == nil ||
		kitDBPostgresServeLogicalName(kitDBNodeCatalogFaultTargetStore, config) != wantName ||
		config.database.databaseID != entry.DatabaseID {
		t.Fatalf("recovered rename serve = served:%t config:%#v", served, config)
	}
	target, err := kitDBForRequest(tenant, kitDBNodeCatalogFaultTargetStore, nil).database()
	if err != nil {
		t.Fatal(err)
	}
	value, found, getErr := target.database.Get([]byte("fault/sentinel"))
	target.Release()
	if getErr != nil || !found || string(value) != "durable" {
		t.Fatalf("renamed crash target sentinel = %q found=%t err=%v", value, found, getErr)
	}
	if stats := manager.fleet.Stats(); stats.ActiveLeases != 0 {
		t.Fatalf("rename recovery retained %d active lease(s)", stats.ActiveLeases)
	}
}

func assertKitDBNodeCatalogFaultActive(
	t *testing.T,
	tenant *Tenant,
	entries map[string]kitDBNodeCatalogEntry,
	topology kitDBNodeCatalogFaultTopology,
	expectSentinel bool,
) {
	t.Helper()
	if len(entries) != 1 {
		t.Fatalf("recovered active entries = %#v", entries)
	}
	entry, found := entries[kitDBNodeCatalogFaultTargetName]
	if !found || entry.State != kitDBNodeCatalogActive || entry.DatabaseID == "" {
		t.Fatalf("recovered active entry = %#v", entry)
	}
	if entry.DatabaseID == topology.sourceID {
		t.Fatalf("recovered target reused source identity %q", topology.sourceID)
	}
	_, config, served := resolveServe(tenant, kitDBNodeCatalogFaultTargetStore)
	if !served || config.database == nil || config.database.databaseID != entry.DatabaseID ||
		config.token != kitDBNodeCatalogFaultToken || config.access != "readwrite" {
		t.Fatalf("recovered active capability = served:%t config:%#v", served, config)
	}
	target, err := kitDBForRequest(tenant, kitDBNodeCatalogFaultTargetStore, nil).database()
	if err != nil {
		t.Fatal(err)
	}
	targetID := target.database.ID()
	if targetID != entry.DatabaseID {
		target.Release()
		t.Fatalf("target identity = %q, want %q", targetID, entry.DatabaseID)
	}
	if err := target.database.Verify(); err != nil {
		target.Release()
		t.Fatalf("verify recovered target: %v", err)
	}
	value, found, err := target.database.Get([]byte("fault/sentinel"))
	if catalog, catalogErr := target.database.Catalog(); catalogErr != nil {
		target.Release()
		t.Fatalf("read recovered target catalog: %v", catalogErr)
	} else if !expectSentinel && len(catalog.Structs) != 0 {
		target.Release()
		t.Fatalf("empty target catalog = %#v", catalog.Structs)
	}
	target.Release()
	if err != nil {
		t.Fatalf("target sentinel lookup: %v", err)
	}
	if expectSentinel && (!found || string(value) != "durable") {
		t.Fatalf("target sentinel = %q found=%t", value, found)
	}
	if !expectSentinel && found {
		t.Fatalf("empty target unexpectedly contains source sentinel %q", value)
	}
}

func assertKitDBNodeCatalogFaultStorageMissing(t *testing.T, path string) {
	t.Helper()
	for _, candidate := range []string{path, path + ".wal", path + ".lock", path + ".history"} {
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("removed catalog target path %q error = %v", candidate, err)
		}
	}
}

func kitDBNodeCatalogFaultCreateStage(stage string) bool {
	switch stage {
	case kitDBNodeCatalogFaultCreateAfterIntent,
		kitDBNodeCatalogFaultCreateAfterTarget,
		kitDBNodeCatalogFaultCreateAfterActive:
		return true
	default:
		return false
	}
}

func kitDBNodeCatalogFaultKnownStage(stage string) bool {
	if kitDBNodeCatalogFaultRenameStage(stage) {
		return true
	}
	for _, candidate := range kitDBNodeCatalogFaultStages {
		if candidate == stage {
			return true
		}
	}
	return false
}

func kitDBNodeCatalogFaultRenameStage(stage string) bool {
	return stage == kitDBNodeCatalogFaultRenameBeforeCatalog ||
		stage == kitDBNodeCatalogFaultRenameAfterCatalog
}

func kitDBNodeCatalogFaultExpectsActive(stage string) bool {
	switch stage {
	case kitDBNodeCatalogFaultCreateAfterTarget,
		kitDBNodeCatalogFaultCreateAfterActive,
		kitDBNodeCatalogFaultDropAfterHide:
		return true
	case kitDBNodeCatalogFaultCreateAfterIntent,
		kitDBNodeCatalogFaultDropAfterDropping,
		kitDBNodeCatalogFaultDropAfterStorage,
		kitDBNodeCatalogFaultDropAfterCatalog:
		return false
	default:
		panic(fmt.Sprintf("unknown node catalog fault stage %q", stage))
	}
}
