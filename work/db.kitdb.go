package work

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/kitwork/engine/app"
	kitdbengine "github.com/kitwork/engine/kitdb"
	requestscope "github.com/kitwork/engine/request"
	"github.com/kitwork/engine/value"
)

const (
	kitDBResourceName       = "database:kitdb"
	kitDBDefaultFile        = "app.kitdb"
	kitDBDocumentLimit      = 8 << 20
	kitDBJSONDepthLimit     = 128
	kitDBJSONNodeLimit      = 100_000
	kitDBCheckpointWALBytes = 8 << 20
	kitDBCheckpointChanges  = 4096
)

// kitDatabaseHandle is private plumbing for the struct ORM. Application code
// cannot access raw key/value operations; database.struct() + database.kitdb()
// is the single Kitwork-facing storage contract.
type kitDatabaseHandle struct {
	tenant       *Tenant
	requestScope *requestscope.Scope
	path         string
}

func kitDBForRequest(tenant *Tenant, relative string, scope *requestscope.Scope) *kitDatabaseHandle {
	relative = kitDBRel(relative)
	return &kitDatabaseHandle{
		tenant: tenant, requestScope: scope,
		path: tenant.resolve(".data", filepath.FromSlash(relative)),
	}
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
	manager, err := appKitDBManager(runtime)
	if err != nil {
		return nil, err
	}
	return manager.open(handle.path)
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
	closed    bool
	databases map[string]*managedKitDB
}

type managedKitDB struct {
	writeMu  sync.Mutex
	database *kitdbengine.DB
}

func appKitDBManager(runtime *app.Runtime) (*kitDBManager, error) {
	if current := runtime.Resource(kitDBResourceName); current != nil {
		manager, ok := current.(*kitDBManager)
		if !ok {
			return nil, fmt.Errorf("kitdb: app resource has incompatible type %T", current)
		}
		return manager, nil
	}
	candidate := &kitDBManager{databases: make(map[string]*managedKitDB)}
	current, installed, err := runtime.InstallResource(kitDBResourceName, candidate)
	if err != nil {
		return nil, err
	}
	if installed {
		return candidate, nil
	}
	manager, ok := current.(*kitDBManager)
	if !ok {
		return nil, fmt.Errorf("kitdb: app resource has incompatible type %T", current)
	}
	return manager, nil
}

func (manager *kitDBManager) open(path string) (*managedKitDB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("kitdb: resolve database path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, fmt.Errorf("kitdb: app database manager is closed")
	}
	if current := manager.databases[absolute]; current != nil {
		return current, nil
	}
	opened, err := kitdbengine.Open(absolute)
	if err != nil {
		return nil, err
	}
	managed := &managedKitDB{database: opened}
	manager.databases[absolute] = managed
	return managed, nil
}

func (manager *kitDBManager) Close() {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	manager.closed = true
	paths := make([]string, 0, len(manager.databases))
	for path := range manager.databases {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	databases := manager.databases
	manager.databases = nil
	manager.mu.Unlock()
	for _, path := range paths {
		managed := databases[path]
		managed.writeMu.Lock()
		_ = managed.database.Close()
		managed.writeMu.Unlock()
	}
}

func checkpointKitDBBeforeWrite(database *kitdbengine.DB) error {
	stats, err := database.Stats()
	if err != nil {
		return err
	}
	if stats.WALBytes < kitDBCheckpointWALBytes && stats.OverlayMutations < kitDBCheckpointChanges {
		return nil
	}
	_, err = database.Checkpoint()
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
	return value.Value{K: value.Invalid, V: message}
}
