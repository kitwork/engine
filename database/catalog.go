package database

import (
	"sort"
	"sync"
)

// The host's catalogue of named databases: app.databases({ … }) of application.work (KITWORK_DATABASE.md
// mục 1, "Host và tên database"). An app picks an entry by its name with connection("warehouse") in its
// .shared/schema/. The host's own connections (app.postgresql, app.connect: the system database the
// queue and cron stores use, Configs) are not in it, so app code never reaches them.
var catalog = struct {
	sync.RWMutex
	entries map[string]Config
}{entries: map[string]Config{}}

// SetCatalog replaces the catalogue with entries, by name, at boot. restore puts back the catalogue it
// replaced (tests, and a boot that fails before it serves).
func SetCatalog(entries map[string]Config) (restore func()) {
	next := make(map[string]Config, len(entries))
	for name, entry := range entries {
		entry.Alias = name
		next[name] = entry
	}
	catalog.Lock()
	previous := catalog.entries
	catalog.entries = next
	catalog.Unlock()
	return func() {
		catalog.Lock()
		catalog.entries = previous
		catalog.Unlock()
	}
}

// CatalogEntry is the database the host declared under name.
func CatalogEntry(name string) (Config, bool) {
	catalog.RLock()
	defer catalog.RUnlock()
	entry, ok := catalog.entries[name]
	return entry, ok
}

// CatalogNames lists the names of the catalogue, sorted.
func CatalogNames() []string {
	catalog.RLock()
	defer catalog.RUnlock()
	names := make([]string, 0, len(catalog.entries))
	for name := range catalog.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
