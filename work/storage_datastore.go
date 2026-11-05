package work

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/database/statement"
	queuestore "github.com/kitwork/engine/utilities/queue"
)

// datastore() is the app's own store at .database/storage/ (KITWORK_STORAGE.md mục 2, 22/10/2026): a database
// of its own, not a table of the app's database, so db never reads it and it is never part of a transaction
// of db. Since 30/10/2026 it is the SQLite file .database/storage/kitwork.db (the schema public of the database
// storage, KITWORK_KITDB.md mục 3), one row of kitwork_storage a value: its key, its encoded value and when it
// expires, in milliseconds since the epoch (NULL for never). The file is reached through the app's pool of it, as the runs of the queue reach
// theirs (runs_database.go), and a write is one transaction begun IMMEDIATE under the file's gate. An expired
// value is gone for every read, and is removed when the store would be full and something kept has expired;
// full, a write fails and nothing kept is pushed out. The KitDB file it was before, storage.kitdb, is never
// opened: the app says it is there while what it holds has not been copied (sayDatastoreLeftBehind).

// The ceilings of datastore() of one app (mục 9; the numbers are open, mục 10.2). Vars so tests can lower them.
var (
	storageDatastoreKeys  = 100_000
	storageDatastoreBytes = 256 << 20
)

// datastoreDatabaseName names the pool of the file among the app's pools.
const datastoreDatabaseName = "@storage"

// datastoreSchema makes the table of datastore() in its file, with the index a sweep finds what has expired by.
// A plain rowid table, no trigger: what the writer of KitDB on SQLite can keep the history of (kitdb/sqlite).
var datastoreSchema = []string{
	`CREATE TABLE IF NOT EXISTS kitwork_storage (key BLOB NOT NULL PRIMARY KEY, value BLOB NOT NULL, expire INTEGER)`,
	`CREATE INDEX IF NOT EXISTS kitwork_storage_expire ON kitwork_storage (expire) WHERE expire IS NOT NULL`,
}

// datastoreLegacyFile is the KitDB file datastore() was until 30/10/2026, beside kitwork.db.
const datastoreLegacyFile = "storage.kitdb"

// datastorePlace is datastore() of one app: whether its table was made in this process, and the count of its
// keys and bytes the ceilings are measured on, kept by its writes in this process (the file has one owner
// process, dblock.go). Expired values are counted as kept until a sweep removes them.
type datastorePlace struct {
	mu      sync.Mutex
	ready   atomic.Bool
	counted bool
	keys    int
	bytes   int64
}

func (p *placeSet) openDatastore(t *Tenant) (storagePlace, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: app đang dừng", errStorageUnavailable)
	}
	if p.datastore == nil {
		p.datastore = &datastorePlace{}
	}
	place := p.datastore
	p.mu.Unlock()
	// an app with routes only boots no tenant of its own: its first use of datastore() says it instead
	p.legacySaid.Do(t.sayDatastoreLeftBehindNow)
	return datastoreAccess{place: place, tenant: t}, nil
}

func (d *datastorePlace) close() {}

// datastoreAccess is datastore() as one tenant of the app reaches it: the file is found from the tenant.
type datastoreAccess struct {
	place  *datastorePlace
	tenant *Tenant
}

// datastorePath is the file of datastore() of t's app.
func datastorePath(t *Tenant) string {
	return t.resolveApp(".database", "storage", statement.PublicFile)
}

// database is the app's pool of the file. With create false, a file never made yet, or one without the table,
// is found false with no error (it holds nothing) and nothing is made; with create true the folder and the
// table are made first.
func (a datastoreAccess) database(create bool) (queuestore.Immediate, bool, error) {
	path := datastorePath(a.tenant)
	if !a.tenant.insideAppRoot(path) {
		return queuestore.Immediate{}, false, fmt.Errorf("%w: .database/storage/ ở ngoài thư mục của app", errStorageUnavailable)
	}
	config := hostSQLiteConfig(datastoreDatabaseName, path)
	pool := a.tenant.lookupDatabase(config)
	if pool == nil {
		if _, err := os.Stat(path); err != nil {
			if !os.IsNotExist(err) {
				return queuestore.Immediate{}, false, fmt.Errorf("%w: .database/storage/ của app: %v", errStorageUnavailable, err)
			}
			if !create {
				return queuestore.Immediate{}, false, nil
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return queuestore.Immediate{}, false, fmt.Errorf("%w: không tạo được .database/storage/ của app: %v", errStorageUnavailable, err)
			}
		}
		opened, err := a.tenant.openDatabase(config)
		if err != nil {
			return queuestore.Immediate{}, false, fmt.Errorf("%w: không mở được .database/storage/ của app: %v", errStorageUnavailable, err)
		}
		pool = opened
	}
	database := queuestore.Immediate{Pool: pool, Gate: queuestore.FileGate(path)}
	if a.place.ready.Load() {
		return database, true, nil
	}
	if !create {
		var held int
		err := pool.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'kitwork_storage'`).Scan(&held)
		if err != nil || held == 0 {
			return queuestore.Immediate{}, false, datastoreError(err)
		}
		a.place.ready.Store(true)
		return database, true, nil
	}
	err := database.Write(func(db queuestore.SQL) error {
		for _, statement := range datastoreSchema {
			if _, err := db.Exec(statement); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return queuestore.Immediate{}, false, datastoreError(err)
	}
	a.place.ready.Store(true)
	return database, true, nil
}

// datastoreError is err of the file as storage says it: UNAVAILABLE when another writer held the file past
// the busy timeout, the error itself otherwise (DATABASE).
func datastoreError(err error) error {
	var coded interface{ Code() int }
	if err != nil && errors.As(err, &coded) && coded.Code()&0xff == 5 {
		return fmt.Errorf("%w: .database/storage/ của app đang bận: %v", errStorageUnavailable, err)
	}
	return err
}

// datastoreExpire is when a value expires as the table keeps it: milliseconds since the epoch, NULL for never.
// A value expires within the millisecond its expiry falls in, read and swept alike: now >= expire, in
// milliseconds.
func datastoreExpire(expireAt time.Time) any {
	if expireAt.IsZero() {
		return nil
	}
	return expireAt.UnixMilli()
}

func datastoreExpiry(expire sql.NullInt64) time.Time {
	if !expire.Valid {
		return time.Time{}
	}
	return time.UnixMilli(expire.Int64)
}

func storageExpired(expireAt, now time.Time) bool {
	return !expireAt.IsZero() && !now.Before(expireAt)
}

// read is the live value of key: data, when it expires, found; a value that has expired is not found. The key
// is always bound as bytes: the column holds BLOBs, and a TEXT parameter never equals a BLOB in SQLite.
func (a datastoreAccess) read(key string) ([]byte, time.Time, bool, error) {
	database, found, err := a.database(false)
	if err != nil || !found {
		return nil, time.Time{}, false, err
	}
	var data []byte
	var expire sql.NullInt64
	err = database.Read(func(db queuestore.SQL) error {
		return db.QueryRow(`SELECT value, expire FROM kitwork_storage WHERE key = ?`, []byte(key)).Scan(&data, &expire)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, datastoreError(err)
	}
	expireAt := datastoreExpiry(expire)
	if storageExpired(expireAt, storageClock()) {
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
	place := a.place
	place.mu.Lock()
	defer place.mu.Unlock()
	database, _, err := a.database(true)
	if err != nil {
		return err
	}
	if err := place.count(database); err != nil {
		return err
	}
	stored := []byte(key)
	keys, size, err := place.after(database, stored, data)
	if err != nil {
		return err
	}
	if keys > storageDatastoreKeys || size > int64(storageDatastoreBytes) {
		// what has expired is gone already: sweep it before refusing, when the table holds any
		held, err := place.expiredHeld(database)
		if err != nil {
			return err
		}
		if held {
			if err := place.sweep(database); err != nil {
				return err
			}
			if keys, size, err = place.after(database, stored, data); err != nil {
				return err
			}
		}
		if keys > storageDatastoreKeys || size > int64(storageDatastoreBytes) {
			return fmt.Errorf("%w: datastore() của app đã tới trần %d khoá, %d byte; lần ghi hỏng, không gì đã lưu bị xoá (KITWORK_STORAGE.md mục 9)", errStorageFull, storageDatastoreKeys, storageDatastoreBytes)
		}
	}
	err = database.Write(func(db queuestore.SQL) error {
		_, err := db.Exec(`INSERT INTO kitwork_storage (key, value, expire) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, expire = excluded.expire`, stored, data, datastoreExpire(expireAt))
		return err
	})
	if err != nil {
		return datastoreError(err)
	}
	place.keys, place.bytes = keys, size
	return nil
}

func (a datastoreAccess) remove(key string) (bool, error) {
	place := a.place
	place.mu.Lock()
	defer place.mu.Unlock()
	database, found, err := a.database(false)
	if err != nil || !found {
		return false, err
	}
	if err := place.count(database); err != nil {
		return false, err
	}
	stored := []byte(key)
	var size int64
	var expire sql.NullInt64
	removed := false
	err = database.Write(func(db queuestore.SQL) error {
		err := db.QueryRow(`SELECT length(key) + length(value), expire FROM kitwork_storage WHERE key = ?`, stored).Scan(&size, &expire)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := db.Exec(`DELETE FROM kitwork_storage WHERE key = ?`, stored); err != nil {
			return err
		}
		removed = true
		return nil
	})
	if err != nil || !removed {
		return false, datastoreError(err)
	}
	place.keys--
	place.bytes -= size
	return !storageExpired(datastoreExpiry(expire), storageClock()), nil
}

// after is the count of keys and bytes once stored holds data. Called with the place's lock.
func (d *datastorePlace) after(database queuestore.Immediate, stored, data []byte) (int, int64, error) {
	keys, size := d.keys, d.bytes+int64(len(stored)+len(data))
	var previous int64
	err := database.Read(func(db queuestore.SQL) error {
		return db.QueryRow(`SELECT length(key) + length(value) FROM kitwork_storage WHERE key = ?`, stored).Scan(&previous)
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		keys++
	case err != nil:
		return 0, 0, datastoreError(err)
	default:
		size -= previous
	}
	return keys, size, nil
}

// count measures what the file holds, once per process. Called with the place's lock.
func (d *datastorePlace) count(database queuestore.Immediate) error {
	if d.counted {
		return nil
	}
	err := database.Read(func(db queuestore.SQL) error {
		return db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length(key) + length(value)), 0) FROM kitwork_storage`).Scan(&d.keys, &d.bytes)
	})
	if err != nil {
		return datastoreError(err)
	}
	d.counted = true
	return nil
}

// expiredHeld reports whether the table holds a value that has expired, found by the index on expire: a store
// full of values that never expire is refused without reading all it holds.
func (d *datastorePlace) expiredHeld(database queuestore.Immediate) (bool, error) {
	var held bool
	err := database.Read(func(db queuestore.SQL) error {
		return db.QueryRow(`SELECT EXISTS (SELECT 1 FROM kitwork_storage WHERE expire IS NOT NULL AND expire <= ?)`, storageClock().UnixMilli()).Scan(&held)
	})
	return held, datastoreError(err)
}

// sweep removes every value that has expired, and counts what is left in the same transaction.
func (d *datastorePlace) sweep(database queuestore.Immediate) error {
	if storageSwept != nil {
		storageSwept("datastore")
	}
	now := storageClock().UnixMilli()
	var keys int
	var size int64
	err := database.Write(func(db queuestore.SQL) error {
		if _, err := db.Exec(`DELETE FROM kitwork_storage WHERE expire IS NOT NULL AND expire <= ?`, now); err != nil {
			return err
		}
		return db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length(key) + length(value)), 0) FROM kitwork_storage`).Scan(&keys, &size)
	})
	if err != nil {
		return datastoreError(err)
	}
	d.keys, d.bytes, d.counted = keys, size, true
	return nil
}

// sayDatastoreLeftBehind says, once for each run of the app, that the KitDB file datastore() was before
// 30/10/2026 is still there and no longer read: what it holds stays in it, and the engine no longer copies it
// (kitdb_legacy.go). It is said at the boot of an app that has background work (the only apps that boot a tenant of their own),
// or else at the app's first use of datastore() (openDatastore).
func (t *Tenant) sayDatastoreLeftBehind() {
	shared, err := t.storage()
	if err != nil {
		return
	}
	shared.places.legacySaid.Do(t.sayDatastoreLeftBehindNow)
}

// sayDatastoreLeftBehindNow looks at the folder and at the mark of a copy only: the KitDB file is never opened,
// kitwork.db is never made, and neither is looked at when its path leads out of the app.
func (t *Tenant) sayDatastoreLeftBehindNow() {
	legacy := t.resolveApp(".database", "storage", datastoreLegacyFile)
	if !t.insideAppRoot(legacy) || !t.insideAppRoot(datastorePath(t)) {
		return
	}
	if info, err := os.Stat(legacy); err != nil || info.IsDir() {
		return
	}
	if datastoreCopied(t) {
		return
	}
	fmt.Println(datastoreLegacyLine)
}

// datastoreLegacyLine is what boot says of an app whose datastore() is still in the KitDB file.
const datastoreLegacyLine = "[Storage] .database/storage/storage.kitdb (KitDB cũ) không còn đọc: datastore() nay ở .database/storage/kitwork.db; giá trị cũ ở lại trong file đó; engine không còn chép file KitDB (05/11/2026): `kitwork migrate kitdb` của một bản engine trước ngày đó chép chúng sang"

// datastoreCopied reports whether the app's kitwork.db of datastore() holds the mark of a copy from the KitDB
// file (kitwork_copies, kitdb_legacy.go).
func datastoreCopied(t *Tenant) bool {
	return kitDBCopyMarked(t, datastoreDatabaseName, datastorePath(t), datastoreLegacySource)
}

// datastoreLegacySource is the KitDB file of datastore() as the mark of a copy names it.
const datastoreLegacySource = ".database/storage/" + datastoreLegacyFile
