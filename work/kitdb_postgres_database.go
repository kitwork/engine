package work

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/pgwire"
	requestscope "github.com/kitwork/engine/request"
)

func (session *kitDBPostgresSession) executeKitDBPostgresCreateDatabase(
	ctx context.Context,
	plan *kitSQLCreateDatabase,
) (pgwire.Result, error) {
	if plan == nil {
		return pgwire.Result{}, pgwire.NewError("42601", "CREATE DATABASE plan is missing")
	}
	if !session.nodeMode {
		return pgwire.Result{}, pgwire.NewError(
			"0A000",
			"CREATE DATABASE requires the KitDB PostgreSQL node listener",
		)
	}
	if !session.maintenance {
		return pgwire.Result{}, pgwire.NewError(
			"0A000",
			"CREATE DATABASE must run from the KitDB maintenance database",
		)
	}
	if err := validateKitDBPostgresDatabaseName(plan.name); err != nil {
		return pgwire.Result{}, pgwire.NewError("42602", err.Error())
	}
	if plan.name == session.databaseName {
		return pgwire.Result{}, pgwire.NewError("42P04", "cannot replace the KitDB maintenance database")
	}
	if plan.source != "" {
		if err := validateKitDBPostgresDatabaseName(plan.source); err != nil {
			return pgwire.Result{}, pgwire.NewError("42602", err.Error())
		}
		if plan.name == plan.source {
			return pgwire.Result{}, pgwire.NewError("22023", "recovery destination must differ from its source database")
		}
	}
	if plan.capability != "" {
		if err := validateKitDBPostgresDatabaseName(plan.capability); err != nil {
			return pgwire.Result{}, pgwire.NewError("42602", err.Error())
		}
	}
	authority, err := session.kitDBPostgresCreateDatabaseAuthority(plan)
	if err != nil {
		return pgwire.Result{}, err
	}
	manager, err := kitDBManagerForTenant(session.tenant)
	if err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	manager.catalogMu.Lock()
	defer manager.catalogMu.Unlock()
	if session.kitDBPostgresDatabaseNameExists(plan.name) {
		return pgwire.Result{}, pgwire.NewError("42P04", fmt.Sprintf("database %q already exists", plan.name))
	}
	entries, _, err := readKitDBNodeCatalog(ctx, session.tenant, manager)
	if err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	if entry, found := entries[plan.name]; found {
		return pgwire.Result{}, pgwire.NewError(
			"42P04",
			fmt.Sprintf("database %q already exists in %s state", plan.name, entry.State),
		)
	}

	storageName := plan.name + ".kitdb"
	destination := session.tenant.resolve(".data", filepath.FromSlash(storageName))
	if !session.tenant.insideSiteRoot(destination) {
		return pgwire.Result{}, pgwire.NewError("42501", "KitDB recovery destination escapes the tenant site")
	}
	storageExists, err := kitDBNodeCatalogStorageExists(destination)
	if err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	if storageExists {
		return pgwire.Result{}, pgwire.NewError(
			"42P04",
			fmt.Sprintf("database %q already has unregistered storage", plan.name),
		)
	}
	capabilityStorage := authority.config.capabilityStorage
	if capabilityStorage == "" {
		capabilityStorage = authority.storageName
	}
	catalogEntry := newKitDBNodeCatalogEntry(plan.name, capabilityStorage)
	if err := persistKitDBNodeCatalogEntry(ctx, session.tenant, manager, catalogEntry); err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	cleanupIntent := func(cause error) error {
		exists, inspectErr := kitDBNodeCatalogStorageExists(destination)
		if inspectErr != nil || exists {
			return errors.Join(cause, inspectErr)
		}
		return errors.Join(
			cause,
			removeKitDBNodeCatalogEntry(ctx, session.tenant, manager, catalogEntry.Name),
		)
	}

	if !session.tenant.beginRequest() {
		return pgwire.Result{}, cleanupIntent(
			pgwire.NewError("57P01", "KitDB tenant is shutting down"),
		)
	}
	defer session.tenant.endRequest()
	lease, err := session.tenant.generationLease()
	if err != nil {
		return pgwire.Result{}, cleanupIntent(pgwire.NewError("57P01", err.Error()))
	}
	request := (&http.Request{}).WithContext(ctx)
	scope := requestscope.New(session.tenant, nil, request)
	if lease != nil && !scope.AddCleanup(lease.Release) {
		lease.Release()
		scope.Close()
		return pgwire.Result{}, cleanupIntent(
			pgwire.NewError("57P01", "KitDB request scope is unavailable"),
		)
	}
	defer scope.Close()

	databaseID := ""
	recoveryTransaction := uint64(0)
	if plan.source != "" {
		managed, err := kitDBForRequest(session.tenant, authority.storageName, scope).database()
		if err != nil {
			if errors.Is(err, kitdbnode.ErrOptionsMismatch) {
				return pgwire.Result{}, cleanupIntent(
					pgwire.NewError("55006", "KitDB source database is busy while recovery history is being enabled; retry the command"),
				)
			}
			return pgwire.Result{}, cleanupIntent(kitDBPostgresError(err))
		}
		result, forkErr := managed.database.ForkToTime(ctx, destination, plan.timestamp)
		managed.Release()
		if forkErr != nil {
			return pgwire.Result{}, cleanupIntent(kitDBPostgresRecoveryError(authority.name, forkErr))
		}
		databaseID = result.DatabaseID
		recoveryTransaction = result.Transaction
	} else {
		databaseID, err = createEmptyKitDBPostgresDatabase(ctx, manager, destination)
		if err != nil {
			return pgwire.Result{}, cleanupIntent(kitDBPostgresError(err))
		}
	}
	target := &dbProxy{
		tenant: session.tenant, engine: "kitdb", dbName: storageName,
		databaseID: databaseID,
		tables:     map[string]map[string]*ColumnSpec{},
		structs:    map[string]*StructDef{},
	}
	registerSchema(target)
	if err := target.ensureKitDBCatalogLoaded(); err != nil {
		detail := fmt.Sprintf("KitDB database %q could not load its catalog: %v", plan.name, err)
		if plan.source != "" {
			detail = fmt.Sprintf(
				"KitDB recovered database %q at transaction %d but could not load its catalog: %v",
				plan.name,
				recoveryTransaction,
				err,
			)
		}
		return pgwire.Result{}, pgwire.NewError(
			"XX000",
			detail,
		)
	}
	catalogEntry.State = kitDBNodeCatalogActive
	catalogEntry.DatabaseID = databaseID
	if err := persistKitDBNodeCatalogEntry(ctx, session.tenant, manager, catalogEntry); err != nil {
		action := "created"
		if plan.source != "" {
			action = "recovered"
		}
		return pgwire.Result{}, pgwire.NewError(
			"XX000",
			fmt.Sprintf("KitDB %s database %q but could not publish its node catalog: %v", action, plan.name, err),
		)
	}
	registerManagedServeFromCapability(
		target,
		authority.config.token,
		authority.config.access,
		capabilityStorage,
		plan.name,
	)
	registered := kitDBPostgresDatabase{
		name: plan.name, storageName: storageName,
		config: serveConfig{
			token: authority.config.token, access: authority.config.access,
			engine: "kitdb", database: target, capabilityStorage: capabilityStorage,
		},
	}
	session.databases = append(session.databases, registered)
	session.databaseNames = append(session.databaseNames, plan.name)
	sort.Strings(session.databaseNames)
	return pgwire.Result{CommandTag: "CREATE DATABASE"}, nil
}

func createEmptyKitDBPostgresDatabase(
	ctx context.Context,
	manager *kitDBManager,
	destination string,
) (string, error) {
	managed, err := manager.openWithOptions(ctx, destination, kitdbengine.OpenOptions{})
	if err != nil {
		return "", err
	}
	defer managed.Release()
	if err := managed.database.Verify(); err != nil {
		return "", err
	}
	if _, err := managed.database.Catalog(); err != nil {
		return "", err
	}
	return managed.database.ID(), nil
}

func (session *kitDBPostgresSession) kitDBPostgresCreateDatabaseAuthority(
	plan *kitSQLCreateDatabase,
) (kitDBPostgresDatabase, error) {
	if plan.source != "" {
		source, found := findKitDBPostgresDatabase(session.databases, plan.source)
		if !found || source.config.database == nil || source.config.engine != "kitdb" {
			return kitDBPostgresDatabase{}, pgwire.NewError(
				"3D000",
				fmt.Sprintf("KitDB source database %q does not exist or is not authorized", plan.source),
			)
		}
		if source.config.access != "readwrite" {
			return kitDBPostgresDatabase{}, pgwire.NewError(
				"42501",
				fmt.Sprintf("KitDB source database %q is not writable", source.name),
			)
		}
		if !kitDBPostgresDatabaseIsCurrent(session.tenant, source) {
			return kitDBPostgresDatabase{}, pgwire.NewError(
				"3D000",
				fmt.Sprintf("KitDB source database %q was renamed or removed", plan.source),
			)
		}
		return source, nil
	}

	capabilities := make([]kitDBPostgresDatabase, 0, len(session.databases))
	for _, database := range session.databases {
		if database.config.database == nil || database.config.engine != "kitdb" ||
			!database.config.database.sourceDeclared || database.config.dropping {
			continue
		}
		capabilities = append(capabilities, database)
	}
	sort.Slice(capabilities, func(left, right int) bool {
		return capabilities[left].name < capabilities[right].name
	})
	if plan.capability != "" {
		for _, capability := range capabilities {
			if capability.name != plan.capability {
				continue
			}
			if capability.config.access != "readwrite" {
				return kitDBPostgresDatabase{}, pgwire.NewError(
					"42501",
					fmt.Sprintf("KitDB capability %q is not writable", capability.name),
				)
			}
			return capability, nil
		}
		return kitDBPostgresDatabase{}, pgwire.NewError(
			"42501",
			fmt.Sprintf("KitDB capability %q does not exist or is not authorized", plan.capability),
		)
	}
	if len(capabilities) == 0 {
		return kitDBPostgresDatabase{}, pgwire.NewError(
			"42501",
			"CREATE DATABASE requires an authorized KitDB source capability",
		)
	}
	if len(capabilities) > 1 {
		names := make([]string, len(capabilities))
		for index, capability := range capabilities {
			names[index] = capability.name
		}
		return kitDBPostgresDatabase{}, pgwire.NewError(
			"42725",
			fmt.Sprintf(
				"CREATE DATABASE is ambiguous across KitDB capabilities %s; use WITH CAPABILITY <name>",
				strings.Join(names, ", "),
			),
		)
	}
	if capabilities[0].config.access != "readwrite" {
		return kitDBPostgresDatabase{}, pgwire.NewError(
			"42501",
			fmt.Sprintf("KitDB capability %q is not writable", capabilities[0].name),
		)
	}
	return capabilities[0], nil
}

func (session *kitDBPostgresSession) executeKitDBPostgresRenameDatabase(
	ctx context.Context,
	plan *kitSQLRenameDatabase,
) (pgwire.Result, error) {
	if plan == nil {
		return pgwire.Result{}, pgwire.NewError("42601", "ALTER DATABASE plan is missing")
	}
	if !session.nodeMode {
		return pgwire.Result{}, pgwire.NewError(
			"0A000",
			"ALTER DATABASE requires the KitDB PostgreSQL node listener",
		)
	}
	if !session.maintenance {
		return pgwire.Result{}, pgwire.NewError(
			"0A000",
			"ALTER DATABASE must run from the KitDB maintenance database",
		)
	}
	if err := validateKitDBPostgresDatabaseName(plan.name); err != nil {
		return pgwire.Result{}, pgwire.NewError("42602", err.Error())
	}
	if err := validateKitDBPostgresDatabaseName(plan.newName); err != nil {
		return pgwire.Result{}, pgwire.NewError("42602", err.Error())
	}
	if plan.name == plan.newName {
		return pgwire.Result{}, pgwire.NewError("42P04", "database rename source and destination match")
	}
	target, found := findKitDBPostgresDatabase(session.databases, plan.name)
	if !found || target.config.database == nil || target.config.engine != "kitdb" ||
		!kitDBPostgresDatabaseIsCurrent(session.tenant, target) {
		return pgwire.Result{}, pgwire.NewError(
			"3D000",
			fmt.Sprintf("database %q does not exist", plan.name),
		)
	}
	if target.config.database.sourceDeclared {
		return pgwire.Result{}, pgwire.NewError(
			"0A000",
			fmt.Sprintf("database %q is source-declared and must be renamed in Kitwork configuration", plan.name),
		)
	}
	if target.config.access != "readwrite" {
		return pgwire.Result{}, pgwire.NewError("25006", fmt.Sprintf("database %q is read-only", plan.name))
	}
	if session.authenticator == nil {
		return pgwire.Result{}, pgwire.NewError("XX000", "KitDB PostgreSQL session registry is unavailable")
	}
	manager, err := kitDBManagerForTenant(session.tenant)
	if err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	manager.catalogMu.Lock()
	defer manager.catalogMu.Unlock()
	entries, exists, err := readKitDBNodeCatalog(ctx, session.tenant, manager)
	if err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	if !exists {
		return pgwire.Result{}, pgwire.NewError("3D000", "KitDB node catalog does not exist")
	}
	entry, found := entries[plan.name]
	if !found {
		return pgwire.Result{}, pgwire.NewError(
			"3D000",
			fmt.Sprintf("database %q is not SQL-managed", plan.name),
		)
	}
	if entry.State != kitDBNodeCatalogActive {
		return pgwire.Result{}, pgwire.NewError(
			"55006",
			fmt.Sprintf("database %q is in %s state", plan.name, entry.State),
		)
	}
	if entry.StorageName != target.storageName {
		return pgwire.Result{}, pgwire.NewError(
			"XX000",
			fmt.Sprintf("database %q storage disagrees with the durable node catalog", plan.name),
		)
	}
	if _, duplicate := entries[plan.newName]; duplicate {
		return pgwire.Result{}, pgwire.NewError(
			"42P04",
			fmt.Sprintf("database %q already exists", plan.newName),
		)
	}
	path := session.tenant.resolve(".data", filepath.FromSlash(target.storageName))
	options, _ := registeredKitDBOpenSettings(session.tenant, target.storageName)
	opened, err := manager.openWithOptions(ctx, path, options)
	if err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	actualID := opened.database.ID()
	opened.Release()
	if !strings.EqualFold(actualID, entry.DatabaseID) {
		return pgwire.Result{}, pgwire.NewError(
			"XX000",
			fmt.Sprintf(
				"database %q identity mismatch: catalog expects %s, file contains %s",
				plan.name,
				entry.DatabaseID,
				actualID,
			),
		)
	}

	session.authenticator.sessionMu.Lock()
	defer session.authenticator.sessionMu.Unlock()
	if active := session.authenticator.databaseSessionsLocked(plan.name); active != 0 {
		return pgwire.Result{}, pgwire.NewError(
			"55006",
			fmt.Sprintf("database %q is being accessed by %d other session(s)", plan.name, active),
		)
	}
	serveRegMu.Lock()
	defer serveRegMu.Unlock()
	targetKey := serveDBKey(session.tenant, target.storageName)
	config, registered := serveReg[targetKey]
	if !registered || config.database != target.config.database || config.dropping ||
		kitDBPostgresServeLogicalName(target.storageName, config) != plan.name {
		return pgwire.Result{}, pgwire.NewError(
			"55006",
			fmt.Sprintf("database %q changed while rename was being prepared", plan.name),
		)
	}
	prefix := tenantScopeKey(session.tenant) + "|"
	for key, candidate := range serveReg {
		if key == targetKey || !strings.HasPrefix(key, prefix) || candidate.engine != "kitdb" {
			continue
		}
		storageName := strings.TrimPrefix(key, prefix)
		if kitDBPostgresServeLogicalName(storageName, candidate) == plan.newName {
			return pgwire.Result{}, pgwire.NewError(
				"42P04",
				fmt.Sprintf("database %q already exists", plan.newName),
			)
		}
	}
	renamed := entry
	renamed.Name = plan.newName
	if err := renamePersistedKitDBNodeCatalogEntry(
		ctx, session.tenant, manager, plan.name, renamed,
	); err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	config.logicalName = plan.newName
	serveReg[targetKey] = config
	session.authenticator.renameDatabaseSessionsLocked(
		target.storageName, plan.name, plan.newName, config,
	)
	return pgwire.Result{CommandTag: "ALTER DATABASE"}, nil
}

func (authenticator *kitDBPostgresAuthenticator) renameDatabaseSessionsLocked(
	storageName string,
	oldName string,
	newName string,
	config serveConfig,
) {
	for session := range authenticator.sessions {
		for index := range session.databases {
			if session.databases[index].storageName != storageName ||
				session.databases[index].name != oldName {
				continue
			}
			session.databases[index].name = newName
			session.databases[index].config = config
		}
		for index, name := range session.databaseNames {
			if name == oldName {
				session.databaseNames[index] = newName
			}
		}
	}
}

func kitDBPostgresDatabaseIsCurrent(tenant *Tenant, database kitDBPostgresDatabase) bool {
	if tenant == nil || database.config.database == nil || database.storageName == "" {
		return false
	}
	_, config, found := resolveServe(tenant, database.storageName)
	return found && config.database == database.config.database &&
		kitDBPostgresServeLogicalName(database.storageName, config) == database.name
}

func (session *kitDBPostgresSession) executeKitDBPostgresDropDatabase(
	ctx context.Context,
	plan *kitSQLDropDatabase,
) (pgwire.Result, error) {
	if plan == nil {
		return pgwire.Result{}, pgwire.NewError("42601", "DROP DATABASE plan is missing")
	}
	if !session.nodeMode {
		return pgwire.Result{}, pgwire.NewError(
			"0A000",
			"DROP DATABASE requires the KitDB PostgreSQL node listener",
		)
	}
	if err := validateKitDBPostgresDatabaseName(plan.name); err != nil {
		return pgwire.Result{}, pgwire.NewError("42602", err.Error())
	}
	if plan.name == session.databaseName {
		return pgwire.Result{}, pgwire.NewError(
			"55006",
			fmt.Sprintf("cannot drop the currently open database %q; reconnect to the KitDB maintenance database", plan.name),
		)
	}
	if !session.maintenance {
		return pgwire.Result{}, pgwire.NewError(
			"0A000",
			"DROP DATABASE must run from the KitDB maintenance database",
		)
	}
	target, found := findKitDBPostgresDatabase(session.databases, plan.name)
	if !found || target.config.database == nil || target.config.engine != "kitdb" ||
		!kitDBPostgresDatabaseIsCurrent(session.tenant, target) {
		if plan.ifExists {
			return pgwire.Result{CommandTag: "DROP DATABASE"}, nil
		}
		return pgwire.Result{}, pgwire.NewError("3D000", fmt.Sprintf("database %q does not exist", plan.name))
	}
	if target.config.access != "readwrite" {
		return pgwire.Result{}, pgwire.NewError("25006", fmt.Sprintf("database %q is read-only", plan.name))
	}
	if target.config.database.kitDBHasSourceDeclarations() {
		return pgwire.Result{}, pgwire.NewError(
			"0A000",
			fmt.Sprintf("database %q contains source-declared structs and cannot be dropped through SQL", plan.name),
		)
	}
	if session.authenticator == nil {
		return pgwire.Result{}, pgwire.NewError("XX000", "KitDB PostgreSQL session registry is unavailable")
	}
	path := session.tenant.resolve(".data", filepath.FromSlash(target.storageName))
	if !session.tenant.insideSiteRoot(path) {
		return pgwire.Result{}, pgwire.NewError("42501", "KitDB drop target escapes the tenant site")
	}
	if !session.tenant.beginRequest() {
		return pgwire.Result{}, pgwire.NewError("57P01", "KitDB tenant is shutting down")
	}
	defer session.tenant.endRequest()
	lease, err := session.tenant.generationLease()
	if err != nil {
		return pgwire.Result{}, pgwire.NewError("57P01", err.Error())
	}
	if lease != nil {
		defer lease.Release()
	}
	manager, err := kitDBManagerForTenant(session.tenant)
	if err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	manager.catalogMu.Lock()
	defer manager.catalogMu.Unlock()
	entries, _, err := readKitDBNodeCatalog(ctx, session.tenant, manager)
	if err != nil {
		return pgwire.Result{}, kitDBPostgresError(err)
	}
	catalogEntry, catalogManaged := entries[plan.name]
	if !catalogManaged {
		dependents := kitDBNodeCatalogCapabilityDependents(entries, target.storageName)
		if len(dependents) != 0 {
			visible := dependents
			suffix := ""
			if len(visible) > 8 {
				visible = visible[:8]
				suffix = fmt.Sprintf(" and %d more", len(dependents)-len(visible))
			}
			return pgwire.Result{}, pgwire.NewError(
				"2BP01",
				fmt.Sprintf(
					"database %q grants access to SQL-managed database(s) %s%s; drop those databases first",
					plan.name,
					strings.Join(visible, ", "),
					suffix,
				),
			)
		}
	}
	if catalogManaged {
		if catalogEntry.State != kitDBNodeCatalogActive {
			return pgwire.Result{}, pgwire.NewError(
				"55006",
				fmt.Sprintf("database %q is in %s state", plan.name, catalogEntry.State),
			)
		}
		if catalogEntry.StorageName != target.storageName {
			return pgwire.Result{}, pgwire.NewError(
				"XX000",
				fmt.Sprintf("database %q storage disagrees with the durable node catalog", plan.name),
			)
		}
		options, _ := registeredKitDBOpenSettings(session.tenant, target.storageName)
		opened, err := manager.openWithOptions(ctx, path, options)
		if err != nil {
			return pgwire.Result{}, kitDBPostgresError(err)
		}
		actualID := opened.database.ID()
		opened.Release()
		if !strings.EqualFold(actualID, catalogEntry.DatabaseID) {
			return pgwire.Result{}, pgwire.NewError(
				"XX000",
				fmt.Sprintf(
					"database %q identity mismatch: catalog expects %s, file contains %s",
					plan.name,
					catalogEntry.DatabaseID,
					actualID,
				),
			)
		}
	}

	session.authenticator.sessionMu.Lock()
	active := session.authenticator.databaseSessionsLocked(plan.name)
	if active != 0 {
		session.authenticator.sessionMu.Unlock()
		return pgwire.Result{}, pgwire.NewError(
			"55006",
			fmt.Sprintf("database %q is being accessed by %d other session(s)", plan.name, active),
		)
	}
	_, registered, markErr := beginDropServe(session.tenant, target.storageName, target.config.database)
	session.authenticator.sessionMu.Unlock()
	if markErr != nil {
		return pgwire.Result{}, pgwire.NewError("55006", markErr.Error())
	}
	if !registered {
		if plan.ifExists {
			return pgwire.Result{CommandTag: "DROP DATABASE"}, nil
		}
		return pgwire.Result{}, pgwire.NewError("3D000", fmt.Sprintf("database %q does not exist", plan.name))
	}
	cancelExposure := true
	defer func() {
		if cancelExposure {
			cancelDropServe(session.tenant, target.storageName, target.config.database)
		}
	}()
	if catalogManaged {
		catalogEntry.State = kitDBNodeCatalogDropping
		if err := persistKitDBNodeCatalogEntry(ctx, session.tenant, manager, catalogEntry); err != nil {
			return pgwire.Result{}, kitDBPostgresError(err)
		}
	}
	if err := manager.drop(ctx, path); err != nil {
		if errors.Is(err, kitdbengine.ErrDatabaseNotFound) && (plan.ifExists || catalogManaged) {
			// The logical capability still needs to disappear when its physical
			// files were already removed by an operator.
		} else {
			if catalogManaged {
				catalogEntry.State = kitDBNodeCatalogActive
				if restoreErr := persistKitDBNodeCatalogEntry(
					context.WithoutCancel(ctx),
					session.tenant,
					manager,
					catalogEntry,
				); restoreErr != nil {
					cancelExposure = false
					return pgwire.Result{}, errors.Join(
						kitDBPostgresDropError(plan.name, err),
						fmt.Errorf("restore active node-catalog state: %w", restoreErr),
					)
				}
			}
			return pgwire.Result{}, kitDBPostgresDropError(plan.name, err)
		}
	}
	cancelExposure = false
	if catalogManaged {
		if err := removeKitDBNodeCatalogEntry(ctx, session.tenant, manager, plan.name); err != nil {
			return pgwire.Result{}, kitDBPostgresError(err)
		}
	}

	finishDropServe(session.tenant, target.storageName, target.config.database)
	session.removeKitDBPostgresDatabase(plan.name)
	return pgwire.Result{CommandTag: "DROP DATABASE"}, nil
}

func (session *kitDBPostgresSession) removeKitDBPostgresDatabase(name string) {
	databases := session.databases[:0]
	for _, database := range session.databases {
		if database.name != name {
			databases = append(databases, database)
		}
	}
	session.databases = databases
	names := session.databaseNames[:0]
	for _, databaseName := range session.databaseNames {
		if databaseName != name {
			names = append(names, databaseName)
		}
	}
	session.databaseNames = names
}

func kitDBPostgresDropError(name string, err error) error {
	switch {
	case errors.Is(err, kitdbnode.ErrDatabaseBusy), errors.Is(err, kitdbengine.ErrWriterLocked):
		return pgwire.NewError("55006", fmt.Sprintf("database %q is busy: %v", name, err))
	case errors.Is(err, kitdbengine.ErrDatabaseNotFound):
		return pgwire.NewError("3D000", fmt.Sprintf("database %q does not exist", name))
	default:
		return kitDBPostgresError(err)
	}
}

func (session *kitDBPostgresSession) kitDBPostgresDatabaseNameExists(name string) bool {
	if name == session.databaseName && session.maintenance {
		return true
	}
	for _, entry := range listServes(session.tenant, "kitdb") {
		if kitDBPostgresServeLogicalName(entry.name, entry.config) == name {
			return true
		}
	}
	return false
}

func validateKitDBPostgresDatabaseName(name string) error {
	if name == "" || len(name) > 63 || !utf8.ValidString(name) {
		return fmt.Errorf("invalid KitDB database name %q", name)
	}
	for index, character := range name {
		if character == '_' || unicode.IsLetter(character) || index > 0 && unicode.IsDigit(character) {
			continue
		}
		return fmt.Errorf("invalid KitDB database name %q; use letters, digits and underscores", name)
	}
	if strings.EqualFold(name, "kitdb") {
		return fmt.Errorf("database name %q is reserved for KitDB maintenance", name)
	}
	return nil
}

func kitDBPostgresRecoveryError(source string, err error) error {
	switch {
	case errors.Is(err, kitdbengine.ErrRestoreExists):
		return pgwire.NewError("42P04", "recovery destination already exists")
	case errors.Is(err, kitdbengine.ErrHistoryDisabled):
		return pgwire.NewError(
			"55000",
			fmt.Sprintf("KitDB recovery history is disabled for %q; declare kitdb(..., { recovery: true }) before the time you need to recover", source),
		)
	case errors.Is(err, kitdbengine.ErrRecoveryTimeUnavailable):
		return pgwire.NewError("55000", fmt.Sprintf("KitDB source %q has no durable timestamps for the requested recovery window", source))
	case errors.Is(err, kitdbengine.ErrRecoveryTargetTooOld):
		return pgwire.NewError("22008", err.Error())
	default:
		return kitDBPostgresError(err)
	}
}
