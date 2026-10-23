package work

import (
	"fmt"
	"sync"
	"time"

	"github.com/kitwork/engine/storage"
)

// storagePlace is one of the three places of an app's storage: values kept encoded by encoded key, each
// with the time it expires (zero for never). An expired value is gone for every read.
type storagePlace interface {
	get(key string) ([]byte, bool, error)
	set(key string, data []byte, expireAt time.Time) error
	expiry(key string) (time.Time, bool, error)
	remove(key string) (bool, error)
}

// memoryPlace is memory(): the app's RAM store.
type memoryPlace struct{ store *storage.MemoryStore }

func (m memoryPlace) get(key string) ([]byte, bool, error) {
	data, found := m.store.Get(key)
	return data, found, nil
}

func (m memoryPlace) set(key string, data []byte, expireAt time.Time) error {
	m.store.Set(key, data, expireAt)
	return nil
}

func (m memoryPlace) expiry(key string) (time.Time, bool, error) {
	at, found := m.store.Expiry(key)
	return at, found, nil
}

func (m memoryPlace) remove(key string) (bool, error) { return m.store.Delete(key), nil }

// placeSet holds disk() and datastore() of an app, opened on first use, closed with the app.
type placeSet struct {
	mu        sync.Mutex
	disk      *diskPlace
	datastore *datastorePlace
	closed    bool
}

// place is the store of where for the app, as t (a tenant of the app) reaches it.
func (s *appStorage) place(where storage.Place, t *Tenant) (storagePlace, error) {
	switch where {
	case storage.Memory:
		return memoryPlace{store: s.memory}, nil
	case storage.Disk:
		return s.places.openDisk(s.root)
	case storage.Datastore:
		return s.places.openDatastore(t)
	}
	return nil, fmt.Errorf("không có nơi lưu %q", where)
}

func (p *placeSet) close() {
	p.mu.Lock()
	p.closed = true
	disk, datastore := p.disk, p.datastore
	p.disk, p.datastore = nil, nil
	p.mu.Unlock()
	if disk != nil {
		disk.close()
	}
	if datastore != nil {
		datastore.close()
	}
}
