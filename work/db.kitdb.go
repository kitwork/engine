package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/kitwork/engine/app"
	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

const (
	kitDBResourceName               = "database:kitdb"
	kitDBDefaultFile                = "app.kitdb"
	kitDBTransactionConflictCode    = "KITDB_TRANSACTION_CONFLICT"
	kitDBDocumentLimit              = 8 << 20
	kitDBJSONDepthLimit             = 128
	kitDBJSONNodeLimit              = 100_000
	kitDBCheckpointSoftWALBytes     = 4 << 20
	kitDBCheckpointSoftChanges      = 2048
	kitDBCheckpointHardWALBytes     = 8 << 20
	kitDBCheckpointHardChanges      = 4096
	kitDBBulkCheckpointSoftWALBytes = 256 << 20
	kitDBBulkCheckpointSoftChanges  = 131_072
	kitDBBulkCheckpointHardWALBytes = 384 << 20
	kitDBBulkCheckpointHardChanges  = 196_608
)

type kitDBCheckpointPolicy struct {
	softWALBytes int64
	softChanges  int
	hardWALBytes int64
	hardChanges  int
}

var (
	kitDBDefaultCheckpointPolicy = kitDBCheckpointPolicy{
		softWALBytes: kitDBCheckpointSoftWALBytes,
		softChanges:  kitDBCheckpointSoftChanges,
		hardWALBytes: kitDBCheckpointHardWALBytes,
		hardChanges:  kitDBCheckpointHardChanges,
	}
	kitDBBulkCheckpointPolicy = kitDBCheckpointPolicy{
		softWALBytes: kitDBBulkCheckpointSoftWALBytes,
		softChanges:  kitDBBulkCheckpointSoftChanges,
		hardWALBytes: kitDBBulkCheckpointHardWALBytes,
		hardChanges:  kitDBBulkCheckpointHardChanges,
	}
)

var (
	errKitDBTransactionConflict = errors.New("kitdb: transaction conflict")
	errKitDBStaleSchema         = errors.New("stale schema")
	errKitDBLayoutAdvanced      = errors.New("layout advanced")
)

type kitDBTransactionConflictError struct {
	base    uint64
	current uint64
}

func (err *kitDBTransactionConflictError) Error() string {
	return fmt.Sprintf(
		"kitdb: transaction conflict: database advanced from transaction %d to %d; retry",
		err.base,
		err.current,
	)
}

func (err *kitDBTransactionConflictError) Unwrap() error {
	return errKitDBTransactionConflict
}

// kitDatabaseHandle is private plumbing for the struct ORM. Application code
// cannot access raw key/value operations; database.struct() + database.kitdb()
// is the single Kitwork-facing storage contract.
type kitDatabaseHandle struct {
	tenant       *Tenant
	requestScope *requestscope.Scope
	path         string
	options      kitdbengine.OpenOptions
	expectedID   string
}

func kitDBForRequest(tenant *Tenant, relative string, scope *requestscope.Scope) *kitDatabaseHandle {
	relative = kitDBRel(relative)
	options, expectedID := registeredKitDBOpenSettings(tenant, relative)
	return &kitDatabaseHandle{
		tenant: tenant, requestScope: scope,
		path:    tenant.resolve(".data", filepath.FromSlash(relative)),
		options: options, expectedID: expectedID,
	}
}

func registeredKitDBOpenSettings(
	tenant *Tenant,
	name string,
) (kitdbengine.OpenOptions, string) {
	if tenant == nil {
		return kitdbengine.OpenOptions{}, ""
	}
	key := tenantScopeKey(tenant) + "|kitdb|" + name
	schemaRegMu.Lock()
	proxy := schemaReg[key]
	schemaRegMu.Unlock()
	if proxy == nil {
		return kitdbengine.OpenOptions{}, ""
	}
	return kitdbengine.OpenOptions{
		RetainHistory:    proxy.retainHistory,
		HistoryRetention: proxy.historyRetention,
	}, proxy.databaseID
}

func kitDBRel(relative string) string {
	relative = strings.TrimSpace(strings.ReplaceAll(relative, "\\", "/"))
	relative = strings.TrimPrefix(relative, "/")
	if relative == "" {
		return kitDBDefaultFile
	}
	clean := filepath.ToSlash(filepath.Clean(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, ":") {
		base := filepath.Base(clean)
		if base == "." || base == ".." || base == "/" || base == "" {
			return kitDBDefaultFile
		}
		return base
	}
	return clean
}

func (handle *kitDatabaseHandle) database() (*managedKitDB, error) {
	if handle == nil || handle.tenant == nil {
		return nil, fmt.Errorf("kitdb: tenant is unavailable")
	}
	if !handle.tenant.insideSiteRoot(handle.path) {
		return nil, fmt.Errorf("kitdb: database path escapes the tenant site")
	}
	runtime := handle.tenant.AppRuntime()
	if runtime == nil {
		return nil, fmt.Errorf("kitdb: app runtime is unavailable")
	}
	fleet, fleetErr, configured := handle.tenant.kitDBNode()
	if configured && fleetErr != nil {
		return nil, fleetErr
	}
	if configured && fleet == nil {
		return nil, fmt.Errorf("kitdb: host node manager is unavailable")
	}
	manager, err := appKitDBManager(runtime, fleet)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if handle.requestScope != nil && handle.requestScope.Context() != nil {
		ctx = handle.requestScope.Context()
	}
	managed, err := manager.openWithOptions(ctx, handle.path, handle.options)
	if err != nil {
		return nil, err
	}
	if handle.expectedID != "" && !strings.EqualFold(managed.database.ID(), handle.expectedID) {
		actual := managed.database.ID()
		managed.Release()
		return nil, fmt.Errorf(
			"kitdb: database identity mismatch for %q: catalog expects %s, file contains %s",
			handle.path,
			handle.expectedID,
			actual,
		)
	}
	return managed, nil
}

// ensureKitDBCatalogLoaded merges durable, catalog-only structs into this
// generation's proxy exactly once. Source declarations remain authoritative
// for migration planning; persisted definitions fill only names absent from
// the application schema.
func (proxy *dbProxy) ensureKitDBCatalogLoaded() error {
	if proxy == nil || proxy.engine != "kitdb" {
		return nil
	}
	proxy.catalogOnce.Do(func() {
		managed, err := kitDBForRequest(proxy.tenant, proxy.dbName, proxy.scope).database()
		if err != nil {
			proxy.catalogErr = err
			return
		}
		defer managed.Release()
		snapshot, err := managed.database.Catalog()
		if err != nil {
			proxy.catalogErr = err
			return
		}

		pendingRows := false
		pendingIndexes := false
		proxy.schemaMu.Lock()
		byID := make(map[string]string, len(proxy.structs))
		for name, definition := range proxy.structs {
			if definition != nil {
				byID[definition.ID] = name
			}
		}
		for _, entry := range snapshot.Structs {
			stored, err := decodeKitDBCatalog(entry.Definition, entry.Name)
			if err != nil {
				proxy.catalogErr = err
				break
			}
			if _, found, err := loadKitDBRowMigrationState(managed.database, stored); err != nil {
				proxy.catalogErr = err
				break
			} else if found {
				pendingRows = true
			}
			if pending, err := kitDBDefinitionHasPendingSecondaryIndex(
				managed.database, stored,
			); err != nil {
				proxy.catalogErr = err
				break
			} else if pending {
				pendingIndexes = true
			}
			if declared := proxy.structs[entry.Name]; declared != nil {
				if declared.ID != entry.ID {
					proxy.catalogErr = fmt.Errorf(
						"kitdb: catalog struct %q has ID %s but the application declares %s",
						entry.Name, entry.ID, declared.ID,
					)
					break
				}
				continue
			}
			if declaredName := byID[entry.ID]; declaredName != "" && declaredName != entry.Name {
				proxy.catalogErr = fmt.Errorf(
					"kitdb: catalog struct ID %s is named %q but the application declares %q",
					entry.ID, entry.Name, declaredName,
				)
				break
			}
			// An accepted physical index transition persists its target beside the
			// active catalog. Hydrating that target makes catalog-only writers keep
			// the shadow index current after a restart while the planner still hides
			// it until the catalog cutover transaction commits.
			if pending, found, err := kitDBPendingIndexDefinition(managed.database, stored); err != nil {
				proxy.catalogErr = err
				break
			} else if found {
				stored = pending
			}
			proxy.structs[entry.Name] = stored
			proxy.tables[entry.Name] = stored.columns
			byID[entry.ID] = entry.Name
		}
		if proxy.catalogErr == nil {
			proxy.catalogPending = proxy.catalogPending || pendingRows || pendingIndexes
			proxy.resolveForeignKeysLocked()
			for _, definition := range proxy.structs {
				if err := validateStructForeignConstraints(definition, proxy.structs); err != nil {
					proxy.catalogErr = fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
					break
				}
			}
			if proxy.catalogErr == nil {
				proxy.catalogTx = snapshot.Transaction
				proxy.catalogRev = snapshot.Revision
			}
		}
		proxy.schemaMu.Unlock()
		if proxy.catalogErr == nil && pendingRows {
			_, _ = scheduleKitDBRowMigration(managed)
		}
		if proxy.catalogErr == nil && pendingIndexes {
			_, _ = scheduleKitDBSecondaryIndex(managed)
		}
	})
	return proxy.catalogErr
}

func (proxy *dbProxy) markKitDBCatalogPending() {
	if proxy == nil || proxy.engine != "kitdb" {
		return
	}
	proxy.schemaMu.Lock()
	proxy.catalogPending = true
	proxy.schemaMu.Unlock()
}

func (proxy *dbProxy) kitDBStructIsSourceDeclared(name string) bool {
	if proxy == nil {
		return false
	}
	proxy.schemaMu.RLock()
	defer proxy.schemaMu.RUnlock()
	for _, declared := range proxy.declaredTables {
		if strings.EqualFold(declared, name) {
			return true
		}
	}
	return false
}

func (proxy *dbProxy) kitDBHasSourceDeclarations() bool {
	if proxy == nil || !proxy.sourceDeclared {
		return false
	}
	proxy.schemaMu.RLock()
	defer proxy.schemaMu.RUnlock()
	return len(proxy.declaredTables) != 0
}

// refreshKitDBCatalogDefinitions compares the allocation-light kernel revision
// before decoding catalog definitions. A changed revision replaces the whole
// catalog-owned portion atomically, so rename/drop cannot leave stale names in
// another connection or application generation. Source declarations remain
// authoritative and transactions retain their pinned schema snapshot.
func (proxy *dbProxy) refreshKitDBCatalogDefinitions() error {
	if proxy == nil || proxy.engine != "kitdb" {
		return nil
	}
	if err := proxy.ensureKitDBCatalogLoaded(); err != nil {
		return err
	}
	if proxy.transaction != nil {
		return nil
	}
	managed, err := kitDBForRequest(proxy.tenant, proxy.dbName, proxy.scope).database()
	if err != nil {
		return err
	}
	defer managed.Release()
	version, err := managed.database.CatalogVersion()
	if err != nil {
		return err
	}
	proxy.schemaMu.RLock()
	pending := proxy.catalogPending
	observed := proxy.catalogRev
	proxy.schemaMu.RUnlock()
	if !pending && observed == version.Revision {
		return nil
	}

	managed.writeMu.Lock()
	version, err = managed.database.CatalogVersion()
	if err != nil {
		managed.writeMu.Unlock()
		return err
	}
	proxy.schemaMu.RLock()
	pending = proxy.catalogPending
	observed = proxy.catalogRev
	proxy.schemaMu.RUnlock()
	if !pending && observed == version.Revision {
		managed.writeMu.Unlock()
		return nil
	}
	pendingRows, pendingIndexes, err := proxy.replaceKitDBCatalogDefinitionsLocked(managed)
	managed.writeMu.Unlock()
	if err != nil {
		return err
	}
	if pendingRows {
		_, _ = scheduleKitDBRowMigration(managed)
	}
	if pendingIndexes {
		_, _ = scheduleKitDBSecondaryIndex(managed)
	}
	return nil
}

// replaceKitDBCatalogDefinitionsLocked requires the file-owned relational
// writer gate. It builds and validates a complete replacement before taking
// schemaMu, then swaps the maps in one publication step.
func (proxy *dbProxy) replaceKitDBCatalogDefinitionsLocked(
	managed *managedKitDB,
) (bool, bool, error) {
	if proxy == nil || managed == nil || managed.database == nil {
		return false, false, fmt.Errorf("kitdb: catalog refresh is unavailable")
	}
	catalog, err := managed.database.Catalog()
	if err != nil {
		return false, false, err
	}

	proxy.schemaMu.RLock()
	declaredNames := append([]string(nil), proxy.declaredTables...)
	sourceDefinitions := make(map[string]*StructDef, len(declaredNames))
	for _, name := range declaredNames {
		if definition := proxy.structs[name]; definition != nil {
			sourceDefinitions[name] = definition
		}
	}
	proxy.schemaMu.RUnlock()

	nextDefinitions := make(map[string]*StructDef, len(sourceDefinitions)+len(catalog.Structs))
	nextTables := make(map[string]map[string]*ColumnSpec, len(sourceDefinitions)+len(catalog.Structs))
	sourceByName := make(map[string]string, len(sourceDefinitions))
	sourceByID := make(map[string]string, len(sourceDefinitions))
	for name, definition := range sourceDefinitions {
		nextDefinitions[name] = definition
		nextTables[name] = definition.columns
		sourceByName[strings.ToLower(name)] = name
		if definition.ID != "" {
			sourceByID[definition.ID] = name
		}
	}

	pendingRows := false
	pendingIndexes := false
	catalogNames := make(map[string]string, len(catalog.Structs))
	for _, entry := range catalog.Structs {
		key := strings.ToLower(entry.Name)
		if previous := catalogNames[key]; previous != "" && previous != entry.Name {
			return false, false, fmt.Errorf(
				"kitdb: catalog structs %q and %q share a case-insensitive name",
				previous, entry.Name,
			)
		}
		catalogNames[key] = entry.Name
		stored, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return false, false, decodeErr
		}
		if _, found, stateErr := loadKitDBRowMigrationState(managed.database, stored); stateErr != nil {
			return false, false, stateErr
		} else if found {
			pendingRows = true
		}
		if pending, stateErr := kitDBDefinitionHasPendingSecondaryIndex(
			managed.database, stored,
		); stateErr != nil {
			return false, false, stateErr
		} else if pending {
			pendingIndexes = true
		}
		if sourceName := sourceByName[key]; sourceName != "" {
			declared := sourceDefinitions[sourceName]
			if declared.ID != entry.ID {
				return false, false, fmt.Errorf(
					"kitdb: catalog struct %q has ID %s but the application declares %s",
					entry.Name, entry.ID, declared.ID,
				)
			}
			continue
		}
		if sourceName := sourceByID[entry.ID]; sourceName != "" {
			return false, false, fmt.Errorf(
				"kitdb: catalog struct ID %s is named %q but the application declares %q",
				entry.ID, entry.Name, sourceName,
			)
		}
		if pending, found, pendingErr := kitDBPendingIndexDefinition(managed.database, stored); pendingErr != nil {
			return false, false, pendingErr
		} else if found {
			stored = pending
		}
		nextDefinitions[entry.Name] = stored
		nextTables[entry.Name] = stored.columns
	}

	resolver := &dbProxy{tables: nextTables, structs: nextDefinitions}
	resolver.resolveForeignKeys()
	for _, name := range sortedKitDBDefinitionNames(nextDefinitions) {
		definition := nextDefinitions[name]
		if err := validateStructForeignConstraints(definition, nextDefinitions); err != nil {
			return false, false, fmt.Errorf("kitdb: struct %q: %w", definition.Name, err)
		}
	}

	proxy.schemaMu.Lock()
	proxy.structs = nextDefinitions
	proxy.tables = nextTables
	proxy.catalogPending = pendingRows || pendingIndexes
	proxy.catalogTx = catalog.Transaction
	proxy.catalogRev = catalog.Revision
	proxy.schemaMu.Unlock()
	return pendingRows, pendingIndexes, nil
}

// publishKitDBDefinition makes a definition visible after either its catalog
// transaction or its durable physical-index build intent commits. Existing
// readers retain immutable snapshots; the planner hides unfinished indexes.
func (proxy *dbProxy) publishKitDBDefinition(managed *managedKitDB, definition *StructDef) {
	if proxy == nil || definition == nil {
		return
	}
	proxy.publishKitDBDefinitions(managed, map[string]*StructDef{definition.Name: definition})
}

func (proxy *dbProxy) publishKitDBDefinitions(managed *managedKitDB, definitions map[string]*StructDef) {
	if proxy == nil || len(definitions) == 0 {
		return
	}
	version, versionErr := kitDBPublishedCatalogVersion(managed)
	proxy.schemaMu.Lock()
	defer proxy.schemaMu.Unlock()
	for name, definition := range definitions {
		if definition == nil {
			continue
		}
		for previousName, previous := range proxy.structs {
			if previous != nil && previous.ID == definition.ID && previousName != name {
				delete(proxy.structs, previousName)
				delete(proxy.tables, previousName)
			}
		}
		proxy.structs[name] = definition
		proxy.tables[name] = definition.columns
	}
	if versionErr == nil {
		proxy.catalogTx = version.Transaction
		proxy.catalogRev = version.Revision
	} else {
		proxy.catalogTx = 0
		proxy.catalogRev = ""
	}
	proxy.resolveForeignKeysLocked()
}

func (proxy *dbProxy) unpublishKitDBDefinition(managed *managedKitDB, name, id string) {
	if proxy == nil {
		return
	}
	version, versionErr := kitDBPublishedCatalogVersion(managed)
	proxy.schemaMu.Lock()
	defer proxy.schemaMu.Unlock()
	for candidate, definition := range proxy.structs {
		if !strings.EqualFold(candidate, name) ||
			(id != "" && definition != nil && definition.ID != id) {
			continue
		}
		delete(proxy.structs, candidate)
		delete(proxy.tables, candidate)
	}
	if versionErr == nil {
		proxy.catalogTx = version.Transaction
		proxy.catalogRev = version.Revision
	} else {
		proxy.catalogTx = 0
		proxy.catalogRev = ""
	}
	proxy.resolveForeignKeysLocked()
}

// DDL callers hold managed.writeMu from the catalog commit through proxy
// publication. Reading the version inside that boundary binds the in-memory
// maps to exactly the durable catalog they represent.
func kitDBPublishedCatalogVersion(managed *managedKitDB) (kitdbengine.CatalogVersion, error) {
	if managed == nil || managed.database == nil {
		return kitdbengine.CatalogVersion{}, fmt.Errorf("kitdb: catalog publication is unavailable")
	}
	return managed.database.CatalogVersion()
}

func (handle *kitDatabaseHandle) requestError() error {
	if handle == nil {
		return fmt.Errorf("kitdb: capability is unavailable")
	}
	if handle.requestScope == nil || handle.requestScope.Context() == nil {
		return nil
	}
	select {
	case <-handle.requestScope.Context().Done():
		return handle.requestScope.Context().Err()
	default:
		return nil
	}
}

type kitDBManager struct {
	mu        sync.Mutex
	catalogMu sync.Mutex
	closed    bool
	fleet     *kitdbnode.Manager
	ownsFleet bool
	driverErr error
	active    int
	notify    chan struct{}
	closeOnce sync.Once
	closeDone chan struct{}
}

type managedKitDB struct {
	manager  *kitDBManager
	context  context.Context
	path     string
	options  kitdbengine.OpenOptions
	lease    *kitdbnode.Lease
	writeMu  *kitdbnode.DatabaseGate
	database *kitdbengine.DB
	release  sync.Once
}

func appKitDBManager(runtime *app.Runtime, shared *kitdbnode.Manager) (*kitDBManager, error) {
	if current := runtime.Resource(kitDBResourceName); current != nil {
		manager, ok := current.(*kitDBManager)
		if !ok {
			return nil, fmt.Errorf("kitdb: app resource has incompatible type %T", current)
		}
		if shared != nil && manager.fleet != shared {
			return nil, fmt.Errorf("kitdb: app runtime already owns a different node manager")
		}
		return manager, nil
	}
	fleet := shared
	ownsFleet := false
	if fleet == nil {
		var err error
		fleet, err = kitdbnode.NewManager(kitdbnode.Limits{})
		if err != nil {
			return nil, err
		}
		ownsFleet = true
	}
	candidate := newKitDBManager(fleet, ownsFleet)
	current, installed, err := runtime.InstallResource(kitDBResourceName, candidate)
	if err != nil {
		candidate.Close()
		return nil, err
	}
	if installed {
		return candidate, nil
	}
	candidate.Close()
	manager, ok := current.(*kitDBManager)
	if !ok {
		return nil, fmt.Errorf("kitdb: app resource has incompatible type %T", current)
	}
	return manager, nil
}

func newKitDBManager(fleet *kitdbnode.Manager, ownsFleet bool) *kitDBManager {
	manager := &kitDBManager{
		fleet: fleet, ownsFleet: ownsFleet,
		notify: make(chan struct{}), closeDone: make(chan struct{}),
	}
	if fleet == nil {
		manager.driverErr = fmt.Errorf("kitdb: host node manager is unavailable")
	} else {
		manager.driverErr = errors.Join(
			fleet.RegisterRowMigrationDriver(kitDBSharedRowMigrationDriver),
			fleet.RegisterSecondaryIndexDriver(kitDBSharedSecondaryIndexDriver),
		)
	}
	return manager
}

func (manager *kitDBManager) open(ctx context.Context, path string) (*managedKitDB, error) {
	return manager.openWithOptions(ctx, path, kitdbengine.OpenOptions{})
}

func (manager *kitDBManager) openWithOptions(
	ctx context.Context,
	path string,
	options kitdbengine.OpenOptions,
) (*managedKitDB, error) {
	if manager == nil {
		return nil, fmt.Errorf("kitdb: app database manager is unavailable")
	}
	if manager.driverErr != nil {
		return nil, manager.driverErr
	}
	lease, err := manager.fleet.Acquire(ctx, path, options)
	if err != nil {
		return nil, err
	}
	database := lease.DB()
	absolute := database.Path()
	gate := lease.Gate()
	if gate == nil {
		_ = lease.Release()
		return nil, fmt.Errorf("kitdb: node database gate is unavailable")
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		_ = lease.Release()
		return nil, fmt.Errorf("kitdb: app database manager is closed")
	}
	manager.active++
	manager.mu.Unlock()
	return &managedKitDB{
		manager: manager, context: ctx, path: absolute, lease: lease,
		writeMu: gate, database: database, options: options,
	}, nil
}

func (manager *kitDBManager) drop(ctx context.Context, path string) error {
	if manager == nil || manager.fleet == nil {
		return fmt.Errorf("kitdb: app database manager is unavailable")
	}
	if ctx == nil {
		return fmt.Errorf("kitdb: drop context is nil")
	}
	manager.mu.Lock()
	closed := manager.closed
	driverErr := manager.driverErr
	manager.mu.Unlock()
	if closed {
		return fmt.Errorf("kitdb: app database manager is closed")
	}
	if driverErr != nil {
		return driverErr
	}
	return manager.fleet.DropDatabase(ctx, path)
}

func scheduleKitDBRowMigration(managed *managedKitDB) (*kitdbnode.MaintenanceTicket, error) {
	if managed == nil || managed.manager == nil || managed.manager.fleet == nil || managed.database == nil {
		return nil, fmt.Errorf("kitdb: managed database is unavailable")
	}
	return managed.manager.fleet.ScheduleRowMigration(
		context.Background(), managed.path, managed.options,
	)
}

func scheduleKitDBSecondaryIndex(managed *managedKitDB) (*kitdbnode.MaintenanceTicket, error) {
	if managed == nil || managed.manager == nil || managed.manager.fleet == nil || managed.database == nil {
		return nil, fmt.Errorf("kitdb: managed database is unavailable")
	}
	return managed.manager.fleet.ScheduleSecondaryIndex(
		context.Background(), managed.path, managed.options,
	)
}

func (managed *managedKitDB) Release() {
	if managed == nil {
		return
	}
	managed.release.Do(func() {
		managed.manager.mu.Lock()
		if managed.manager.active > 0 {
			managed.manager.active--
		}
		if managed.manager.active == 0 {
			close(managed.manager.notify)
			managed.manager.notify = make(chan struct{})
		}
		managed.manager.mu.Unlock()
		_ = managed.lease.Release()
	})
}

func (manager *kitDBManager) Close() {
	if manager == nil {
		return
	}
	manager.closeOnce.Do(func() {
		manager.mu.Lock()
		manager.closed = true
		fleet := manager.fleet
		ownsFleet := manager.ownsFleet
		for !ownsFleet && manager.active != 0 {
			notify := manager.notify
			manager.mu.Unlock()
			<-notify
			manager.mu.Lock()
		}
		manager.mu.Unlock()
		if ownsFleet && fleet != nil {
			_ = fleet.Close()
		}
		close(manager.closeDone)
	})
	<-manager.closeDone
}

func checkpointKitDBBeforeWrite(managed *managedKitDB) error {
	return checkpointKitDBBeforeWriteWithPolicy(managed, kitDBDefaultCheckpointPolicy)
}

func checkpointKitDBBeforeBulkWrite(managed *managedKitDB) error {
	return checkpointKitDBBeforeWriteWithPolicy(managed, kitDBBulkCheckpointPolicy)
}

// Background row and index maintenance already owns the database gate, so it
// checkpoints inline instead of enqueueing another task for the same file.
// This keeps the WAL-backed overlay bounded across millions of resumable
// chunks without weakening the durability of each committed cursor.
func checkpointKitDBBackgroundMaintenance(
	ctx context.Context,
	database *kitdbengine.DB,
) (bool, error) {
	return checkpointKitDBBackgroundMaintenanceWithPolicy(
		ctx, database, kitDBBulkCheckpointPolicy,
	)
}

func checkpointKitDBBackgroundMaintenanceWithPolicy(
	ctx context.Context,
	database *kitdbengine.DB,
	policy kitDBCheckpointPolicy,
) (bool, error) {
	if database == nil {
		return false, fmt.Errorf("kitdb: maintenance database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	stats, err := database.Stats()
	if err != nil {
		return false, err
	}
	if stats.WALBytes < policy.softWALBytes && stats.OverlayMutations < policy.softChanges {
		return false, nil
	}
	_, err = database.Checkpoint()
	return err == nil, err
}

func checkpointKitDBBeforeWriteWithPolicy(managed *managedKitDB, policy kitDBCheckpointPolicy) error {
	if managed == nil || managed.database == nil || managed.manager == nil || managed.manager.fleet == nil {
		return fmt.Errorf("kitdb: managed database is unavailable")
	}
	stats, err := managed.database.Stats()
	if err != nil {
		return err
	}
	hard := stats.WALBytes >= policy.hardWALBytes ||
		stats.OverlayMutations >= policy.hardChanges
	if !hard && stats.WALBytes < policy.softWALBytes &&
		stats.OverlayMutations < policy.softChanges {
		return nil
	}
	priority := kitdbnode.MaintenanceNormal
	if hard {
		priority = kitdbnode.MaintenanceUrgent
	}
	ctx := managed.context
	if ctx == nil {
		ctx = context.Background()
	}
	ticket, err := managed.manager.fleet.ScheduleCheckpoint(
		ctx, managed.path, managed.options, priority,
	)
	if err != nil {
		if !hard && (errors.Is(err, kitdbnode.ErrMaintenanceQueueFull) ||
			errors.Is(err, kitdbnode.ErrMaintenanceDatabaseQueueFull)) {
			return nil
		}
		return err
	}
	if !hard {
		return nil
	}
	_, err = ticket.Wait(ctx)
	return err
}

func validateKitDBJSON(input value.Value) error {
	type frame struct {
		item  value.Value
		depth int
	}
	pending := []frame{{item: input}}
	visited := 0
	for len(pending) != 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		visited++
		if visited > kitDBJSONNodeLimit {
			return fmt.Errorf("JSON value exceeds %d nodes", kitDBJSONNodeLimit)
		}
		if current.depth > kitDBJSONDepthLimit {
			return fmt.Errorf("JSON value exceeds depth %d", kitDBJSONDepthLimit)
		}
		switch current.item.K {
		case value.Nil, value.Bool, value.Number, value.String, value.Time, value.Duration, value.Bytes:
		case value.Array:
			items := current.item.Array()
			for index := len(items) - 1; index >= 0; index-- {
				pending = append(pending, frame{item: items[index], depth: current.depth + 1})
			}
		case value.Map:
			fields := current.item.Map()
			keys := make([]string, 0, len(fields))
			for key := range fields {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for index := len(keys) - 1; index >= 0; index-- {
				pending = append(pending, frame{item: fields[keys[index]], depth: current.depth + 1})
			}
		default:
			return fmt.Errorf("value kind %s is not JSON data", current.item.K)
		}
	}
	return nil
}

func decodeKitDBValue(encoded []byte) value.Value {
	var decoded value.Value
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return kitDBError(fmt.Errorf("decode stored JSON: %w", err))
	}
	return decoded
}

func kitDBError(err error) value.Value {
	message := err.Error()
	if !strings.HasPrefix(message, "kitdb: ") {
		message = "kitdb: " + message
	}
	result := value.Value{K: value.Invalid, V: message}
	if code := kitDBErrorCode(err); code != "" {
		return value.InvalidFailure(code, message)
	}
	return result
}

func kitDBErrorCode(err error) string {
	if errors.Is(err, errKitDBTransactionConflict) {
		return kitDBTransactionConflictCode
	}
	return ""
}
