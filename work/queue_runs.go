package work

import (
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/relational"
	queuestore "github.com/kitwork/engine/utilities/queue"
)

// The runs of an app's background work live in its own database (KITWORK_QUEUE.md mục 2: "Các lần chạy lưu
// trong kitwork_runs của database của app"): the table kitwork_runs of the KitDB file in
// .database/application/, the file connection().schema({ … }) of .shared/schema/ declares the app's tables
// in. Never a database of the host: a host's system database is often a server other apps and other
// machines share, and an app's runs are the app's data.
//
// The file is reached as the app's ORM reaches it, a lease of the app's KitDB manager for each use (so a
// file nobody uses can close, and the manager never waits on a lease the queue keeps), with KitDB's own SQL
// on top (relational.Attach, OpenNativeSQL; the kernel stays the manager's). A write holds the file's
// writer gate, the one every write of the ORM holds: one writer at a time, which is what makes a claim, a
// settle, a cancel and a replay one step each (utilities/queue).

const runsResourceName = "kitwork:runs"

// runsDatabase is the SQL face of the app's KitDB file, kept by the app runtime while the file stays open:
// the kernel it is attached to, and whether the tables were made in it.
type runsDatabase struct {
	mu     sync.Mutex
	kernel *kitdbengine.DB
	engine *relational.Engine
	db     *sql.DB
	ready  bool
	closed bool
	// seq is the highest seq of kitwork_runs in the file attached, read from the table once (seqRead) and
	// counted on by every run stored after: each is stored under the file's writer gate, by this process
	// alone (the file has one owner), so a send never reads the whole table for its number.
	seq     int64
	seqRead bool
	// writes counts the writes made through it, each one under the file's writer gate: what an idle
	// worker must not add to.
	writes atomic.Int64
}

func (r *runsDatabase) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.detachLocked()
}

func (r *runsDatabase) detachLocked() {
	if r.db != nil {
		_ = r.db.Close()
	}
	if r.engine != nil {
		_ = r.engine.Close()
	}
	r.kernel, r.engine, r.db, r.ready = nil, nil, nil, false
	r.seq, r.seqRead = 0, false
}

// nextSeq is the seq of the next run stored through db, the SQL face attached now; called under the
// file's writer gate.
func (r *runsDatabase) nextSeq(db *sql.DB) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db != db {
		// attached again since the call began: read the table, as a store with no counter does
		highest, err := queuestore.HighestSeq(db)
		return highest + 1, err
	}
	if !r.seqRead {
		highest, err := queuestore.HighestSeq(db)
		if err != nil {
			return 0, err
		}
		r.seq, r.seqRead = highest, true
	}
	r.seq++
	return r.seq, nil
}

// sqlFor gives the SQL face of kernel, attaching it when the file was opened again since (the manager closes
// a file nobody uses; the next lease opens a new kernel).
func (r *runsDatabase) sqlFor(kernel *kitdbengine.DB) (*sql.DB, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, false, fmt.Errorf("kitwork_runs: app đã đóng")
	}
	if r.kernel != kernel {
		r.detachLocked()
		engine, err := relational.Attach(kernel, relational.Options{})
		if err != nil {
			return nil, false, err
		}
		db, err := relational.OpenNativeSQL(engine)
		if err != nil {
			_ = engine.Close()
			return nil, false, err
		}
		r.kernel, r.engine, r.db = kernel, engine, db
	}
	return r.db, r.ready, nil
}

func (r *runsDatabase) markReady(kernel *kitdbengine.DB) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.kernel == kernel {
		r.ready = true
	}
}

// appRuns is the runs database of t's app: queuestore.Database over the app's KitDB file.
type appRuns struct {
	tenant *Tenant
}

func (t *Tenant) runsResource() (*runsDatabase, error) {
	if t == nil || t.appRuntime == nil {
		return nil, fmt.Errorf("kitwork_runs: app runtime is unavailable")
	}
	if current, ok := t.appRuntime.Resource(runsResourceName).(*runsDatabase); ok && current != nil {
		return current, nil
	}
	resource, _, err := t.appRuntime.InstallResource(runsResourceName, &runsDatabase{})
	if err != nil {
		return nil, err
	}
	current, ok := resource.(*runsDatabase)
	if !ok {
		return nil, fmt.Errorf("kitwork_runs: app resource has unexpected type %T", resource)
	}
	return current, nil
}

func (a appRuns) Read(fn func(db *sql.DB) error) error  { return a.use(false, fn) }
func (a appRuns) Write(fn func(db *sql.DB) error) error { return a.use(true, fn) }

// NextSeq numbers a run being stored (queuestore.Sequencer), inside Write.
func (a appRuns) NextSeq(db *sql.DB) (int64, error) {
	resource, err := a.tenant.runsResource()
	if err != nil {
		return 0, err
	}
	return resource.nextSeq(db)
}

// use runs fn on the app's file: a lease for the call, the tables made the first time the file is used, the
// writer gate held for a write and shared for a read.
func (a appRuns) use(write bool, fn func(db *sql.DB) error) error {
	resource, err := a.tenant.runsResource()
	if err != nil {
		return err
	}
	managed, err := kitDBForRequest(a.tenant, kitDBApplicationName, nil).database()
	if err != nil {
		return err
	}
	defer managed.Release()
	db, ready, err := resource.sqlFor(managed.database)
	if err != nil {
		return err
	}
	if !ready {
		managed.writeMu.Lock()
		err := queuestore.InitSchema(db)
		managed.writeMu.Unlock()
		if err != nil {
			return err
		}
		resource.markReady(managed.database)
	}
	if !write {
		managed.writeMu.RLock()
		defer managed.writeMu.RUnlock()
		return fn(db)
	}
	// as before every write of the ORM: a file whose log has grown is checkpointed first
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	resource.writes.Add(1)
	return fn(db)
}

// appID is the key of t's app: its identity (the identity column of entity). Every domain of an app shares
// one .shared/queue/, one .schedule/ and one database, so their runs are the identity's, not a domain's. A
// root app is the stable logical identity "app"; a legacy flat or sites layout falls back to the domain.
func (t *Tenant) appID() string {
	if t.entity.Identity != "" {
		return t.entity.Identity
	}
	if t.config != nil && t.config.layout.IsSingleApp() {
		return RootAppIdentity
	}
	return t.entity.Domain
}

// queueStore is the store of t's app's queue runs.
func (t *Tenant) queueStore() *queuestore.Store { return t.runsStore(queueKind) }

// scheduleStore is the store of t's app's schedule runs (KITWORK_SCHEDULE.md mục 9).
func (t *Tenant) scheduleStore() *queuestore.Store { return t.runsStore(scheduleKind) }

// runsStore is the store of t's app's runs of kind.
func (t *Tenant) runsStore(kind string) *queuestore.Store {
	return queuestore.New(appRuns{tenant: t}, kind)
}
