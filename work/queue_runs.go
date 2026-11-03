package work

import (
	"path/filepath"
	"sync"

	queuestore "github.com/kitwork/engine/utilities/queue"
)

// The runs of an app's background work live in its own database (KITWORK_QUEUE.md mục 2: "Các lần chạy lưu
// trong kitwork_runs của database của app"): the table kitwork_runs of the SQLite file
// .database/application/kitwork.db, the file connection().tables({ … }) of .shared/schema/ declares the app's
// tables in (29/10/2026; before, the KitDB file application.kitdb beside it, which boot no longer opens:
// appKitDBNotCopied). Never a connection of the host's own (app.postgresql({ alias: "system" })): a
// host's system database is often a server other apps and other machines share, and an app's runs are the
// app's data. The config.work of a folder may choose a database of the host's catalogue the app declares
// (queue.database(db), schedule.database(db): runs_database.go); that is the app's choice, made in its own files.
//
// Either way the file is reached as catalogRuns reaches a file of the catalogue: through the app's pool of it,
// the one its statements use, and a write holds the file's gate (queuestore.FileGate): one writer at a time,
// which is what makes a claim, a settle, a cancel and a replay one step each (utilities/queue).

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

// runsStore is the store of t's app's runs of kind, in the database the config.work of its folder chose.
func (t *Tenant) runsStore(kind string) *queuestore.Store {
	worker := t.queueWorker()
	if kind == scheduleKind {
		worker = t.scheduleWorker()
	}
	var place *runsPlace
	if worker != nil {
		place = worker.runs.Load()
	}
	return t.runsStoreAt(kind, place)
}

// runsStoreAt is the store of t's app's runs of kind in place: a database of the host's catalogue, or the
// app's database for nil. A write of it on a database of the catalogue tries once (runsDatabase).
func (t *Tenant) runsStoreAt(kind string, place *runsPlace) *queuestore.Store {
	return queuestore.New(t.runsDatabase(kind, place, nil), kind)
}

// runsDatabase is the database of t's app's runs of kind in place: a database of the host's catalogue, or the
// app's database for nil. stop, when set, is the running worker's: its writes on a database of the catalogue
// (a claim, a settle, a heartbeat) wait for another writer of the file until stop closes; with none (boot's
// sync, a request's send, cancel, replay) a write fails when the busy timeout runs out.
func (t *Tenant) runsDatabase(kind string, place *runsPlace, stop <-chan struct{}) queuestore.Database {
	if place == nil {
		place = t.appRunsPlace()
	}
	return catalogRuns{tenant: t, place: place, kind: kind, stop: stop}
}

// appRunsPlaces holds the place of each app's own database file, so the tables of the runs are made once per
// file (runsPlace.ready).
var appRunsPlaces sync.Map

// appRunsPlace is where the runs live by default (29/10/2026): the app's database, the SQLite file
// .database/application/kitwork.db its statements on connection() write too, through the same pool. The
// KitDB file it was before, application.kitdb, keeps what it held (sayLeftBehind).
func (t *Tenant) appRunsPlace() *runsPlace {
	path := appDatabasePath(t, "")
	if absolute, err := filepath.Abs(path); err == nil {
		path = filepath.Clean(absolute)
	}
	place, _ := appRunsPlaces.LoadOrStore(path, &runsPlace{name: "application", path: path})
	return place.(*runsPlace)
}
