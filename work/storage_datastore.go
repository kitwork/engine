package work

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
)

// datastore() is the app's own store at .database/storage/ (KITWORK_STORAGE.md mục 2, 22/10/2026): a KitDB of
// its own, not a table of the app's database, so db never reads it and it is never part of
// db.transaction(…). Each value is one key of the file, its prefix keeping it apart from what KitDB holds
// for itself, written as when it expires then the value. The file is opened through the app's KitDB
// manager for each operation, as the runs of the queue are (queue_runs.go), and a write holds the file's
// writer gate. An expired value is gone for every read, and is removed when the store would be full and
// something kept can have expired since the last sweep; full, a write fails and nothing kept is pushed out.

// The ceilings of datastore() of one app (mục 9; the numbers are open, mục 10.2). Vars so tests can lower them.
var (
	storageDatastoreKeys  = 100_000
	storageDatastoreBytes = 256 << 20
)

// datastoreFile is the KitDB of datastore() inside the app's .database/storage/.
const datastoreFile = "storage.kitdb"

// datastorePrefix begins every key datastore() writes in its file.
var datastorePrefix = []byte("storage:")

// datastorePlace is datastore() of one app: the count of its keys and bytes the ceilings are measured on,
// and when the soonest value kept expires (zero when none does), kept by its writes in this process (KitDB
// has one writer process).
type datastorePlace struct {
	mu         sync.Mutex
	counted    bool
	keys       int
	bytes      int64
	nextExpiry time.Time
}

func (p *placeSet) openDatastore(t *Tenant) (storagePlace, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("%w: app đang dừng", errStorageUnavailable)
	}
	if p.datastore == nil {
		p.datastore = &datastorePlace{}
	}
	return datastoreAccess{place: p.datastore, tenant: t}, nil
}

func (d *datastorePlace) close() {}

// datastoreAccess is datastore() as one tenant of the app reaches it: the file is found from the tenant.
type datastoreAccess struct {
	place  *datastorePlace
	tenant *Tenant
}

// open leases the app's datastore file for one operation.
func (a datastoreAccess) open() (*managedKitDB, error) {
	handle := &kitDatabaseHandle{
		tenant:    a.tenant,
		path:      a.tenant.resolveApp(".database", "storage", datastoreFile),
		appScoped: true,
	}
	managed, err := handle.database()
	if err != nil {
		return nil, fmt.Errorf("%w: không mở được .database/storage/ của app: %v", errStorageUnavailable, err)
	}
	return managed, nil
}

func datastoreKey(key string) []byte {
	return append(append([]byte(nil), datastorePrefix...), key...)
}

// datastoreRecord is when a value expires (zero for never), then the value.
func datastoreRecord(data []byte, expireAt time.Time) []byte {
	var expire int64
	if !expireAt.IsZero() {
		expire = expireAt.UnixNano()
	}
	out := binary.BigEndian.AppendUint64(make([]byte, 0, 8+len(data)), uint64(expire))
	return append(out, data...)
}

func readDatastoreRecord(record []byte) ([]byte, time.Time, bool) {
	if len(record) < 8 {
		return nil, time.Time{}, false
	}
	var expireAt time.Time
	if expire := int64(binary.BigEndian.Uint64(record)); expire != 0 {
		expireAt = time.Unix(0, expire)
	}
	return record[8:], expireAt, true
}

func storageExpired(expireAt, now time.Time) bool {
	return !expireAt.IsZero() && !now.Before(expireAt)
}

// read is the live value of key: data, when it expires, found; a record that has expired is not found.
func (a datastoreAccess) read(key string) ([]byte, time.Time, bool, error) {
	managed, err := a.open()
	if err != nil {
		return nil, time.Time{}, false, err
	}
	defer managed.Release()
	managed.writeMu.RLock()
	record, found, err := managed.database.Get(datastoreKey(key))
	managed.writeMu.RUnlock()
	if err != nil || !found {
		return nil, time.Time{}, false, err
	}
	data, expireAt, ok := readDatastoreRecord(record)
	if !ok || storageExpired(expireAt, storageClock()) {
		return nil, time.Time{}, false, nil
	}
	return data, expireAt, true, nil
}

func (a datastoreAccess) get(key string) ([]byte, bool, error) {
	data, _, found, err := a.read(key)
	return data, found, err
}

func (a datastoreAccess) expiry(key string) (time.Time, bool, error) {
	_, expireAt, found, err := a.read(key)
	return expireAt, found, err
}

func (a datastoreAccess) set(key string, data []byte, expireAt time.Time) error {
	managed, err := a.open()
	if err != nil {
		return err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	place := a.place
	place.mu.Lock()
	defer place.mu.Unlock()
	if err := place.count(managed.database); err != nil {
		return err
	}
	stored := datastoreKey(key)
	record := datastoreRecord(data, expireAt)
	keys, size, err := place.after(managed.database, stored, record)
	if err != nil {
		return err
	}
	if keys > storageDatastoreKeys || size > int64(storageDatastoreBytes) {
		// what has expired is gone already: sweep it before refusing, when something can have expired
		if storageSweepDue(true, place.nextExpiry) {
			if err := place.sweep(managed.database); err != nil {
				return err
			}
			if keys, size, err = place.after(managed.database, stored, record); err != nil {
				return err
			}
		}
		if keys > storageDatastoreKeys || size > int64(storageDatastoreBytes) {
			return fmt.Errorf("%w: datastore() của app đã tới trần %d khoá, %d byte; lần ghi hỏng, không gì đã lưu bị xoá (KITWORK_STORAGE.md mục 9)", errStorageFull, storageDatastoreKeys, storageDatastoreBytes)
		}
	}
	if err := commitDatastore(managed.database, func(tx *kitdbengine.Tx) error { return tx.Put(stored, record) }); err != nil {
		return err
	}
	place.keys, place.bytes = keys, size
	place.nextExpiry = storageSooner(true, place.nextExpiry, expireAt)
	return nil
}

func (a datastoreAccess) remove(key string) (bool, error) {
	managed, err := a.open()
	if err != nil {
		return false, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return false, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	place := a.place
	place.mu.Lock()
	defer place.mu.Unlock()
	if err := place.count(managed.database); err != nil {
		return false, err
	}
	stored := datastoreKey(key)
	record, found, err := managed.database.Get(stored)
	if err != nil || !found {
		return false, err
	}
	if err := commitDatastore(managed.database, func(tx *kitdbengine.Tx) error { return tx.Delete(stored) }); err != nil {
		return false, err
	}
	place.keys--
	place.bytes -= int64(len(stored) + len(record))
	_, expireAt, ok := readDatastoreRecord(record)
	return ok && !storageExpired(expireAt, storageClock()), nil
}

// after is the count of keys and bytes once stored holds record.
func (d *datastorePlace) after(database *kitdbengine.DB, stored, record []byte) (int, int64, error) {
	keys, size := d.keys, d.bytes+int64(len(stored)+len(record))
	previous, found, err := database.Get(stored)
	if err != nil {
		return 0, 0, err
	}
	if found {
		size -= int64(len(stored) + len(previous))
	} else {
		keys++
	}
	return keys, size, nil
}

// count measures what the file holds, and when the soonest value expires, once per process. Called with the
// place's lock and the writer gate.
func (d *datastorePlace) count(database *kitdbengine.DB) error {
	if d.counted {
		return nil
	}
	keys, size, soonest, _, err := d.measure(database, time.Time{})
	if err != nil {
		return err
	}
	d.keys, d.bytes, d.nextExpiry, d.counted = keys, size, soonest, true
	return nil
}

// measure walks the values of the file once: the keys and bytes of those kept, when the soonest of them
// expires, and, with now given, the keys of those that have expired by then (not counted as kept).
func (d *datastorePlace) measure(database *kitdbengine.DB, now time.Time) (int, int64, time.Time, [][]byte, error) {
	keys, size := 0, int64(0)
	var soonest time.Time
	var gone [][]byte
	err := database.Walk(func(key, record []byte) error {
		if !bytes.HasPrefix(key, datastorePrefix) {
			return nil
		}
		_, expireAt, ok := readDatastoreRecord(record)
		if ok && !now.IsZero() && storageExpired(expireAt, now) {
			gone = append(gone, bytes.Clone(key))
			return nil
		}
		keys++
		size += int64(len(key) + len(record))
		if ok {
			soonest = storageSooner(true, soonest, expireAt)
		}
		return nil
	})
	return keys, size, soonest, gone, err
}

// sweep removes every value that has expired, and counts what is left in the same walk.
func (d *datastorePlace) sweep(database *kitdbengine.DB) error {
	if storageSwept != nil {
		storageSwept("datastore")
	}
	keys, size, soonest, gone, err := d.measure(database, storageClock())
	if err != nil {
		return err
	}
	if len(gone) > 0 {
		err := commitDatastore(database, func(tx *kitdbengine.Tx) error {
			for _, key := range gone {
				if err := tx.Delete(key); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	d.keys, d.bytes, d.nextExpiry, d.counted = keys, size, soonest, true
	return nil
}

// commitDatastore writes what apply puts in one transaction.
func commitDatastore(database *kitdbengine.DB, apply func(tx *kitdbengine.Tx) error) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	if err := apply(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Commit(); err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("%w: %v", errStorageUnavailable, err)
		}
		return err
	}
	return nil
}
