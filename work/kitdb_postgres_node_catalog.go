package work

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb"
)

const (
	kitDBNodeCatalogFile       = ".kitdb-node.kitdb"
	kitDBNodeCatalogVersion    = uint16(2)
	kitDBNodeCatalogVersionV1  = uint16(1)
	kitDBNodeCatalogHeaderSize = uint16(40)
	kitDBNodeCatalogMaxEntries = 4096
	kitDBNodeCatalogMaxValue   = 1024
)

var (
	kitDBNodeCatalogMagic  = [8]byte{'K', 'I', 'T', 'N', 'D', 'C', '0', '1'}
	kitDBNodeCatalogPrefix = []byte("kitdb/node/database/v1/")
	kitDBNodeCatalogCRC    = crc32.MakeTable(crc32.Castagnoli)
)

type kitDBNodeCatalogState uint8

const (
	kitDBNodeCatalogCreating kitDBNodeCatalogState = 1
	kitDBNodeCatalogActive   kitDBNodeCatalogState = 2
	kitDBNodeCatalogDropping kitDBNodeCatalogState = 3
)

type kitDBNodeCatalogEntry struct {
	Name              string
	StorageName       string
	CapabilityStorage string
	DatabaseID        string
	State             kitDBNodeCatalogState
	CreatedAt         int64
}

func newKitDBNodeCatalogEntry(name, capabilityStorage string) kitDBNodeCatalogEntry {
	return kitDBNodeCatalogEntry{
		Name: name, StorageName: name + ".kitdb",
		CapabilityStorage: capabilityStorage,
		State:             kitDBNodeCatalogCreating,
		CreatedAt:         time.Now().UTC().UnixNano(),
	}
}

func (state kitDBNodeCatalogState) String() string {
	switch state {
	case kitDBNodeCatalogCreating:
		return "creating"
	case kitDBNodeCatalogActive:
		return "active"
	case kitDBNodeCatalogDropping:
		return "dropping"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}

func (entry kitDBNodeCatalogEntry) validate() error {
	if err := validateKitDBPostgresDatabaseName(entry.Name); err != nil {
		return err
	}
	if err := validateKitDBNodeCatalogStorageName(entry.StorageName); err != nil {
		return fmt.Errorf("kitdb node catalog: database %q: %w", entry.Name, err)
	}
	if entry.CapabilityStorage == "" || len(entry.CapabilityStorage) > 512 ||
		strings.IndexByte(entry.CapabilityStorage, 0) >= 0 {
		return fmt.Errorf(
			"kitdb node catalog: database %q has invalid capability storage",
			entry.Name,
		)
	}
	if entry.CreatedAt <= 0 {
		return fmt.Errorf("kitdb node catalog: database %q has invalid creation time", entry.Name)
	}
	switch entry.State {
	case kitDBNodeCatalogCreating:
		if entry.DatabaseID != "" {
			return fmt.Errorf("kitdb node catalog: creating database %q already has an identity", entry.Name)
		}
	case kitDBNodeCatalogActive, kitDBNodeCatalogDropping:
		identity, err := hex.DecodeString(entry.DatabaseID)
		if err != nil || len(identity) != 16 {
			return fmt.Errorf("kitdb node catalog: database %q has invalid identity", entry.Name)
		}
	default:
		return fmt.Errorf(
			"kitdb node catalog: database %q has invalid state %d",
			entry.Name,
			entry.State,
		)
	}
	return nil
}

func validateKitDBNodeCatalogStorageName(name string) error {
	const suffix = ".kitdb"
	if name == "" || name == kitDBNodeCatalogFile ||
		strings.ContainsAny(name, `/\\`) || strings.IndexByte(name, 0) >= 0 ||
		!strings.HasSuffix(name, suffix) {
		return fmt.Errorf("invalid storage name %q", name)
	}
	if err := validateKitDBPostgresDatabaseName(strings.TrimSuffix(name, suffix)); err != nil {
		return fmt.Errorf("invalid storage name %q: %w", name, err)
	}
	return nil
}

func encodeKitDBNodeCatalogEntry(entry kitDBNodeCatalogEntry) ([]byte, error) {
	if err := entry.validate(); err != nil {
		return nil, err
	}
	storage := []byte(entry.StorageName)
	capability := []byte(entry.CapabilityStorage)
	identity := []byte(entry.DatabaseID)
	payloadSize := len(storage) + len(capability) + len(identity)
	encodedSize := int(kitDBNodeCatalogHeaderSize) + payloadSize + 4
	if encodedSize > kitDBNodeCatalogMaxValue {
		return nil, fmt.Errorf(
			"kitdb node catalog: database %q metadata exceeds %d bytes",
			entry.Name,
			kitDBNodeCatalogMaxValue,
		)
	}
	encoded := make([]byte, encodedSize)
	copy(encoded[0:8], kitDBNodeCatalogMagic[:])
	binary.LittleEndian.PutUint16(encoded[8:10], kitDBNodeCatalogVersion)
	binary.LittleEndian.PutUint16(encoded[10:12], kitDBNodeCatalogHeaderSize)
	encoded[12] = byte(entry.State)
	binary.LittleEndian.PutUint64(encoded[16:24], uint64(entry.CreatedAt))
	binary.LittleEndian.PutUint16(encoded[24:26], uint16(len(storage)))
	binary.LittleEndian.PutUint16(encoded[26:28], uint16(len(capability)))
	binary.LittleEndian.PutUint16(encoded[28:30], uint16(len(identity)))
	binary.LittleEndian.PutUint32(encoded[32:36], uint32(payloadSize))
	position := int(kitDBNodeCatalogHeaderSize)
	position += copy(encoded[position:], storage)
	position += copy(encoded[position:], capability)
	copy(encoded[position:], identity)
	checksumAt := len(encoded) - 4
	binary.LittleEndian.PutUint32(
		encoded[checksumAt:],
		crc32.Checksum(encoded[:checksumAt], kitDBNodeCatalogCRC),
	)
	return encoded, nil
}

func decodeKitDBNodeCatalogEntry(name string, encoded []byte) (kitDBNodeCatalogEntry, error) {
	var entry kitDBNodeCatalogEntry
	minimum := int(kitDBNodeCatalogHeaderSize) + 4
	if len(encoded) < minimum || len(encoded) > kitDBNodeCatalogMaxValue {
		return entry, fmt.Errorf("kitdb node catalog: database %q has invalid metadata size", name)
	}
	if !bytes.Equal(encoded[0:8], kitDBNodeCatalogMagic[:]) {
		return entry, fmt.Errorf("kitdb node catalog: database %q has invalid metadata magic", name)
	}
	version := binary.LittleEndian.Uint16(encoded[8:10])
	if (version != kitDBNodeCatalogVersionV1 && version != kitDBNodeCatalogVersion) ||
		binary.LittleEndian.Uint16(encoded[10:12]) != kitDBNodeCatalogHeaderSize {
		return entry, fmt.Errorf("kitdb node catalog: database %q uses an unsupported metadata version", name)
	}
	if encoded[13] != 0 || binary.LittleEndian.Uint16(encoded[14:16]) != 0 ||
		binary.LittleEndian.Uint16(encoded[30:32]) != 0 ||
		binary.LittleEndian.Uint32(encoded[36:40]) != 0 {
		return entry, fmt.Errorf("kitdb node catalog: database %q has nonzero reserved metadata", name)
	}
	checksumAt := len(encoded) - 4
	wantChecksum := binary.LittleEndian.Uint32(encoded[checksumAt:])
	if crc32.Checksum(encoded[:checksumAt], kitDBNodeCatalogCRC) != wantChecksum {
		return entry, fmt.Errorf("kitdb node catalog: database %q metadata checksum mismatch", name)
	}
	storageSize := int(binary.LittleEndian.Uint16(encoded[24:26]))
	capabilitySize := int(binary.LittleEndian.Uint16(encoded[26:28]))
	identitySize := int(binary.LittleEndian.Uint16(encoded[28:30]))
	payloadSize := int(binary.LittleEndian.Uint32(encoded[32:36]))
	if payloadSize != storageSize+capabilitySize+identitySize ||
		int(kitDBNodeCatalogHeaderSize)+payloadSize != checksumAt {
		return entry, fmt.Errorf("kitdb node catalog: database %q has invalid metadata lengths", name)
	}
	position := int(kitDBNodeCatalogHeaderSize)
	entry = kitDBNodeCatalogEntry{
		Name:        name,
		StorageName: string(encoded[position : position+storageSize]),
		State:       kitDBNodeCatalogState(encoded[12]),
		CreatedAt:   int64(binary.LittleEndian.Uint64(encoded[16:24])),
	}
	position += storageSize
	entry.CapabilityStorage = string(encoded[position : position+capabilitySize])
	position += capabilitySize
	entry.DatabaseID = string(encoded[position : position+identitySize])
	if version == kitDBNodeCatalogVersionV1 && entry.StorageName != entry.Name+".kitdb" {
		return kitDBNodeCatalogEntry{}, fmt.Errorf(
			"kitdb node catalog: version-1 database %q has invalid storage name %q",
			entry.Name,
			entry.StorageName,
		)
	}
	if err := entry.validate(); err != nil {
		return kitDBNodeCatalogEntry{}, err
	}
	return entry, nil
}

func kitDBNodeCatalogKey(name string) []byte {
	key := make([]byte, len(kitDBNodeCatalogPrefix)+len(name))
	copy(key, kitDBNodeCatalogPrefix)
	copy(key[len(kitDBNodeCatalogPrefix):], name)
	return key
}

func kitDBNodeCatalogName(key []byte) (string, bool) {
	if !bytes.HasPrefix(key, kitDBNodeCatalogPrefix) || len(key) == len(kitDBNodeCatalogPrefix) {
		return "", false
	}
	return string(key[len(kitDBNodeCatalogPrefix):]), true
}

func loadKitDBNodeCatalog(database *kitdb.DB) (map[string]kitDBNodeCatalogEntry, error) {
	if database == nil {
		return nil, fmt.Errorf("kitdb node catalog: database is unavailable")
	}
	entries := make(map[string]kitDBNodeCatalogEntry)
	storageOwners := make(map[string]string)
	err := database.Walk(func(key, encoded []byte) error {
		name, found := kitDBNodeCatalogName(key)
		if !found {
			visible := key
			if len(visible) > 32 {
				visible = visible[:32]
			}
			return fmt.Errorf("kitdb node catalog: unknown key prefix %x", visible)
		}
		if len(entries) >= kitDBNodeCatalogMaxEntries {
			return fmt.Errorf(
				"kitdb node catalog: entry limit %d exceeded",
				kitDBNodeCatalogMaxEntries,
			)
		}
		entry, err := decodeKitDBNodeCatalogEntry(name, encoded)
		if err != nil {
			return err
		}
		if _, duplicate := entries[name]; duplicate {
			return fmt.Errorf("kitdb node catalog: duplicate database %q", name)
		}
		if owner, duplicate := storageOwners[entry.StorageName]; duplicate {
			return fmt.Errorf(
				"kitdb node catalog: databases %q and %q share storage %q",
				owner,
				entry.Name,
				entry.StorageName,
			)
		}
		entries[name] = entry
		storageOwners[entry.StorageName] = entry.Name
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func renameKitDBNodeCatalogEntry(
	ctx context.Context,
	database *kitdb.DB,
	oldName string,
	entry kitDBNodeCatalogEntry,
) error {
	if ctx == nil {
		return fmt.Errorf("kitdb node catalog: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKitDBPostgresDatabaseName(oldName); err != nil {
		return err
	}
	if oldName == entry.Name {
		return fmt.Errorf("kitdb node catalog: rename source and destination match")
	}
	encoded, err := encodeKitDBNodeCatalogEntry(entry)
	if err != nil {
		return err
	}
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	if err := transaction.Delete(kitDBNodeCatalogKey(oldName)); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := transaction.Put(kitDBNodeCatalogKey(entry.Name), encoded); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.Commit()
	return err
}

func sortedKitDBNodeCatalogEntries(entries map[string]kitDBNodeCatalogEntry) []kitDBNodeCatalogEntry {
	ordered := make([]kitDBNodeCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		ordered = append(ordered, entry)
	}
	sort.Slice(ordered, func(first, second int) bool {
		return ordered[first].Name < ordered[second].Name
	})
	return ordered
}

func kitDBNodeCatalogCapabilityDependents(
	entries map[string]kitDBNodeCatalogEntry,
	capabilityStorage string,
) []string {
	dependents := make([]string, 0)
	for _, entry := range entries {
		if entry.CapabilityStorage == capabilityStorage {
			dependents = append(dependents, entry.Name)
		}
	}
	sort.Strings(dependents)
	return dependents
}

func putKitDBNodeCatalogEntry(
	ctx context.Context,
	database *kitdb.DB,
	entry kitDBNodeCatalogEntry,
) error {
	if ctx == nil {
		return fmt.Errorf("kitdb node catalog: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := encodeKitDBNodeCatalogEntry(entry)
	if err != nil {
		return err
	}
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	if err := transaction.Put(kitDBNodeCatalogKey(entry.Name), encoded); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.Commit()
	return err
}

func deleteKitDBNodeCatalogEntry(ctx context.Context, database *kitdb.DB, name string) error {
	if ctx == nil {
		return fmt.Errorf("kitdb node catalog: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKitDBPostgresDatabaseName(name); err != nil {
		return err
	}
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	if err := transaction.Delete(kitDBNodeCatalogKey(name)); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = transaction.Rollback()
		return err
	}
	_, err = transaction.Commit()
	return err
}

func kitDBManagerForTenant(tenant *Tenant) (*kitDBManager, error) {
	if tenant == nil {
		return nil, fmt.Errorf("kitdb node catalog: tenant is unavailable")
	}
	runtime := tenant.AppRuntime()
	if runtime == nil {
		return nil, fmt.Errorf("kitdb node catalog: app runtime is unavailable")
	}
	fleet, fleetErr, configured := tenant.kitDBNode()
	if configured && fleetErr != nil {
		return nil, fleetErr
	}
	if configured && fleet == nil {
		return nil, fmt.Errorf("kitdb node catalog: host node manager is unavailable")
	}
	return appKitDBManager(runtime, fleet)
}

func openKitDBNodeCatalog(
	ctx context.Context,
	tenant *Tenant,
	manager *kitDBManager,
	create bool,
) (*managedKitDB, bool, error) {
	if ctx == nil {
		return nil, false, fmt.Errorf("kitdb node catalog: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if tenant == nil || manager == nil {
		return nil, false, fmt.Errorf("kitdb node catalog: owner is unavailable")
	}
	path := tenant.resolve(".data", filepath.FromSlash(kitDBNodeCatalogFile))
	if !tenant.insideSiteRoot(path) {
		return nil, false, fmt.Errorf("kitdb node catalog: path escapes the tenant site")
	}
	if !create {
		exists, err := kitDBNodeCatalogStorageExists(path)
		if err != nil || !exists {
			return nil, exists, err
		}
	}
	managed, err := manager.openWithOptions(ctx, path, kitdb.OpenOptions{PageCacheBytes: -1})
	if err != nil {
		return nil, false, err
	}
	return managed, true, nil
}

func kitDBNodeCatalogStorageExists(path string) (bool, error) {
	paths := []string{path, path + ".wal", path + ".lock", path + ".history"}
	for _, candidate := range paths {
		_, err := os.Lstat(candidate)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return false, fmt.Errorf("kitdb node catalog: inspect storage: %w", err)
		}
	}
	return false, nil
}

func readKitDBNodeCatalog(
	ctx context.Context,
	tenant *Tenant,
	manager *kitDBManager,
) (map[string]kitDBNodeCatalogEntry, bool, error) {
	managed, exists, err := openKitDBNodeCatalog(ctx, tenant, manager, false)
	if err != nil || !exists {
		return nil, exists, err
	}
	defer managed.Release()
	entries, err := loadKitDBNodeCatalog(managed.database)
	return entries, true, err
}

func persistKitDBNodeCatalogEntry(
	ctx context.Context,
	tenant *Tenant,
	manager *kitDBManager,
	entry kitDBNodeCatalogEntry,
) error {
	managed, _, err := openKitDBNodeCatalog(ctx, tenant, manager, true)
	if err != nil {
		return err
	}
	defer managed.Release()
	entries, err := loadKitDBNodeCatalog(managed.database)
	if err != nil {
		return err
	}
	if _, found := entries[entry.Name]; !found && len(entries) >= kitDBNodeCatalogMaxEntries {
		return fmt.Errorf(
			"kitdb node catalog: entry limit %d reached",
			kitDBNodeCatalogMaxEntries,
		)
	}
	return putKitDBNodeCatalogEntry(ctx, managed.database, entry)
}

func removeKitDBNodeCatalogEntry(
	ctx context.Context,
	tenant *Tenant,
	manager *kitDBManager,
	name string,
) error {
	managed, exists, err := openKitDBNodeCatalog(ctx, tenant, manager, false)
	if err != nil || !exists {
		return err
	}
	defer managed.Release()
	return deleteKitDBNodeCatalogEntry(ctx, managed.database, name)
}

func renamePersistedKitDBNodeCatalogEntry(
	ctx context.Context,
	tenant *Tenant,
	manager *kitDBManager,
	oldName string,
	entry kitDBNodeCatalogEntry,
) error {
	managed, exists, err := openKitDBNodeCatalog(ctx, tenant, manager, false)
	if err != nil || !exists {
		if err == nil {
			err = fmt.Errorf("kitdb node catalog: catalog does not exist")
		}
		return err
	}
	defer managed.Release()
	return renameKitDBNodeCatalogEntry(ctx, managed.database, oldName, entry)
}

func (authenticator *kitDBPostgresAuthenticator) restoreKitDBNodeCatalog(ctx context.Context) error {
	if authenticator == nil || authenticator.tenant == nil {
		return fmt.Errorf("kitdb node catalog: authenticator is unavailable")
	}
	manager, err := kitDBManagerForTenant(authenticator.tenant)
	if err != nil {
		return err
	}
	manager.catalogMu.Lock()
	defer manager.catalogMu.Unlock()

	entries, exists, err := readKitDBNodeCatalog(ctx, authenticator.tenant, manager)
	if err != nil || !exists {
		return err
	}
	capabilities := kitDBNodeCatalogCapabilities(authenticator.tenant)
	for _, entry := range sortedKitDBNodeCatalogEntries(entries) {
		capability, found := capabilities[entry.CapabilityStorage]
		if !found || capability.database == nil {
			return fmt.Errorf(
				"kitdb node catalog: database %q references unavailable capability %q",
				entry.Name,
				entry.CapabilityStorage,
			)
		}
		if _, conflict := capabilities[entry.StorageName]; conflict {
			return fmt.Errorf(
				"kitdb node catalog: SQL-managed database %q conflicts with a source declaration",
				entry.Name,
			)
		}
		path, err := kitDBNodeCatalogDatabasePath(authenticator.tenant, entry)
		if err != nil {
			return err
		}
		switch entry.State {
		case kitDBNodeCatalogCreating:
			identity, found, err := recoverCreatingKitDBNodeCatalogEntry(ctx, manager, path)
			if err != nil {
				return fmt.Errorf("kitdb node catalog: recover creating database %q: %w", entry.Name, err)
			}
			if !found {
				if err := removeKitDBNodeCatalogEntry(ctx, authenticator.tenant, manager, entry.Name); err != nil {
					return err
				}
				removeManagedServe(authenticator.tenant, entry.StorageName)
				continue
			}
			entry.State = kitDBNodeCatalogActive
			entry.DatabaseID = identity
			if err := persistKitDBNodeCatalogEntry(ctx, authenticator.tenant, manager, entry); err != nil {
				return err
			}
		case kitDBNodeCatalogDropping:
			if err := manager.drop(ctx, path); err != nil && !errors.Is(err, kitdb.ErrDatabaseNotFound) {
				return fmt.Errorf("kitdb node catalog: finish dropping database %q: %w", entry.Name, err)
			}
			if err := removeKitDBNodeCatalogEntry(ctx, authenticator.tenant, manager, entry.Name); err != nil {
				return err
			}
			removeManagedServe(authenticator.tenant, entry.StorageName)
			continue
		case kitDBNodeCatalogActive:
			if err := requireKitDBNodeCatalogMainFile(path, entry.Name); err != nil {
				return err
			}
		}
		registerKitDBNodeCatalogDatabase(authenticator.tenant, entry, capability)
	}
	return nil
}

func kitDBNodeCatalogCapabilities(tenant *Tenant) map[string]serveConfig {
	capabilities := make(map[string]serveConfig)
	for _, entry := range listServes(tenant, "kitdb") {
		if entry.config.database == nil || !entry.config.database.sourceDeclared || entry.config.dropping {
			continue
		}
		capabilities[entry.name] = entry.config
	}
	return capabilities
}

func kitDBNodeCatalogDatabasePath(
	tenant *Tenant,
	entry kitDBNodeCatalogEntry,
) (string, error) {
	if err := entry.validate(); err != nil {
		return "", err
	}
	path := tenant.resolve(".data", filepath.FromSlash(entry.StorageName))
	if !tenant.insideSiteRoot(path) {
		return "", fmt.Errorf(
			"kitdb node catalog: database %q path escapes the tenant site",
			entry.Name,
		)
	}
	return path, nil
}

func requireKitDBNodeCatalogMainFile(path, name string) error {
	info, err := os.Stat(path)
	switch {
	case err == nil && info.Mode().IsRegular():
		return nil
	case err == nil:
		return fmt.Errorf("kitdb node catalog: database %q path is not a regular file", name)
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("kitdb node catalog: active database %q is missing its main file", name)
	default:
		return fmt.Errorf("kitdb node catalog: inspect database %q: %w", name, err)
	}
}

func recoverCreatingKitDBNodeCatalogEntry(
	ctx context.Context,
	manager *kitDBManager,
	path string,
) (string, bool, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		exists, storageErr := kitDBNodeCatalogStorageExists(path)
		if storageErr != nil {
			return "", false, storageErr
		}
		if exists {
			return "", false, fmt.Errorf("main file is missing while sidecars remain")
		}
		return "", false, nil
	case err != nil:
		return "", false, err
	case !info.Mode().IsRegular():
		return "", false, fmt.Errorf("database path is not a regular file")
	}
	managed, err := manager.openWithOptions(ctx, path, kitdb.OpenOptions{
		PageCacheBytes: -1,
		VerifyOnOpen:   true,
	})
	if err != nil {
		return "", false, err
	}
	defer managed.Release()
	if _, err := managed.database.Catalog(); err != nil {
		return "", false, err
	}
	return managed.database.ID(), true, nil
}

func registerKitDBNodeCatalogDatabase(
	tenant *Tenant,
	entry kitDBNodeCatalogEntry,
	capability serveConfig,
) *dbProxy {
	target := &dbProxy{
		tenant: tenant, engine: "kitdb", dbName: entry.StorageName,
		databaseID: entry.DatabaseID,
		tables:     map[string]map[string]*ColumnSpec{},
		structs:    map[string]*StructDef{},
	}
	registerSchema(target)
	registerManagedServeFromCapability(
		target,
		capability.token,
		capability.access,
		entry.CapabilityStorage,
		entry.Name,
	)
	return target
}
