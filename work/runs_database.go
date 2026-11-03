package work

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

// ── queue.database(db) and schedule.database(db) (KITWORK_QUEUE.md mục 2, KITWORK_SCHEDULE.md mục 2) ──────
//
// The runs of a kind live in the app's database (queue_runs.go) unless the config.work of its folder hands
// its .database call another database the app declares in .shared/schema/ (24/10/2026):
//
//	// app/.shared/schema/jobs.work
//	export const jobs = connection("jobs").tables({ letters });
//
//	// app/.shared/queue/config.work
//	import * as queue from "kitwork/queue";
//	import { jobs } from "app/.shared/schema/jobs.work";
//	queue.database(jobs);
//
// connection() is the app's database, the default said again; connection("tên") is a name of the host's
// catalogue (app.databases of application.work), a SQLite file today. The engine makes and keeps the tables
// of the runs there (kitwork_runs, kitwork_definitions, kitwork_schedules), so a database of connect({ … })
// is refused, and anything that is not a database. Each kind follows its own call: queue.database moves the
// runs of the jobs and their definitions, schedule.database those of the schedules and their state. Changing
// the database never moves a run: what the old one holds stays there, and boot says how much of it had not
// ended when the old one is the app's (leftBehind).

// runsPlace is the database the runs of a kind live in: the app's own (.database/application/kitwork.db,
// appRunsPlace) or one of the host's catalogue, its name there and its SQLite file.
type runsPlace struct {
	name string
	path string
	// ready is set once the tables were made in the file for this place.
	ready atomic.Bool
	// writes counts the writes made in the file for this place, each under the file's gate: what an idle
	// worker must not add to.
	writes atomic.Int64
}

// readRunsDatabase reads what queue.database(…) or schedule.database(…) was handed as config.work ran: the
// place of the runs, nil for the app's database (connection(), the default said again). A value the call
// does not take is said on the server's output, with where it is written, and the app's database kept
// (refused true). The compiler refuses the same where it can follow the import (compiler/folder_config.go);
// this is what reaches the call through another name.
func readRunsDatabase(setting folderSetting, module, label, where string) (runs *runsPlace, refused bool) {
	stays := "; các lần chạy nằm trong database của app (.database/application/kitwork.db)"
	if len(setting.args) != 1 {
		fmt.Printf("%s %s: %s%s\n", label, where, compiler.FolderDatabaseArity(module), stays)
		return nil, true
	}
	switch handle := setting.args[0].V.(type) {
	case *ManagedDatabase:
		if setting.args[0].K != value.Proxy || handle.proxy == nil {
			break
		}
		if handle.proxy.dbName == applicationDatabaseName {
			return nil, false
		}
		if schema, ownSchema := strings.CutPrefix(handle.proxy.dbName, applicationDatabaseName+"/"); ownSchema && handle.proxy.sqlitePath != "" {
			return &runsPlace{name: schema, path: handle.proxy.sqlitePath}, false
		}
		if handle.proxy.sqlitePath != "" {
			return &runsPlace{name: strings.TrimPrefix(handle.proxy.dbName, catalogDatabasePrefix), path: handle.proxy.sqlitePath}, false
		}
	case *ExternalDatabase:
		fmt.Printf("%s %s: %s%s\n", label, where, compiler.FolderDatabaseExternal(module, ""), stays)
		return nil, true
	}
	fmt.Printf("%s %s: %s%s\n", label, where, compiler.FolderDatabaseNotAHandle(module, ""), stays)
	return nil, true
}

// catalogRunsOwners is which app keeps its runs in each SQLite file of the host's catalogue, by the file:
// kitwork_runs carries no app, so the worker of a second app on the same file would claim the first app's
// runs and fail them as runs of no job. One app per file; its queue and its schedules may share it.
var catalogRunsOwners = struct {
	sync.Mutex
	files map[string]*catalogRunsOwner
}{files: map[string]*catalogRunsOwner{}}

// catalogRunsOwner is the app that keeps its runs in a file, and the kinds of them there.
type catalogRunsOwner struct {
	runtime *app.Runtime
	kinds   map[string]bool
}

// catalogRunsResourceName is the app resource that gives the app's files back when the app closes.
const catalogRunsResourceName = "kitwork:runs:catalog"

// catalogRunsRelease gives back, when its app closes, every file the app keeps runs in.
type catalogRunsRelease struct {
	runtime *app.Runtime
}

func (r *catalogRunsRelease) Close() {
	catalogRunsOwners.Lock()
	defer catalogRunsOwners.Unlock()
	for path, owner := range catalogRunsOwners.files {
		if owner.runtime == r.runtime {
			delete(catalogRunsOwners.files, path)
		}
	}
}

// useRunsPlace makes place the database of the runs of kind for t's app, and gives what the runs live in now:
// place, or nil (the app's database) when place is nil or another app keeps its runs in the same file, which
// is said with where the call is written. The file a kind used before is given back.
func (t *Tenant) useRunsPlace(kind string, place *runsPlace, label, where string) *runsPlace {
	if t == nil || t.appRuntime == nil {
		return nil
	}
	runtime := t.appRuntime
	path := ""
	if place != nil {
		if absolute, err := filepath.Abs(place.path); err == nil {
			path = filepath.Clean(absolute)
		} else {
			path = place.path
		}
	}
	catalogRunsOwners.Lock()
	var heldBy *app.Runtime
	if owner := catalogRunsOwners.files[path]; path != "" && owner != nil && owner.runtime != runtime {
		heldBy, path = owner.runtime, ""
	}
	for file, owner := range catalogRunsOwners.files {
		if owner.runtime == runtime && file != path {
			delete(owner.kinds, kind)
			if len(owner.kinds) == 0 {
				delete(catalogRunsOwners.files, file)
			}
		}
	}
	if path != "" {
		owner := catalogRunsOwners.files[path]
		if owner == nil {
			owner = &catalogRunsOwner{runtime: runtime, kinds: map[string]bool{}}
			catalogRunsOwners.files[path] = owner
		}
		owner.kinds[kind] = true
	}
	catalogRunsOwners.Unlock()
	if heldBy != nil {
		fmt.Printf("%s %s: database %s đã giữ các lần chạy của app %s: kitwork_runs không ghi app nào, nên mỗi database của danh mục giữ các lần chạy của một app; host khai một tên khác cho app này. Các lần chạy nằm trong database của app (.database/application/kitwork.db)\n",
			label, where, place.name, heldBy.ID())
		return nil
	}
	if place != nil {
		if _, _, err := runtime.InstallResource(catalogRunsResourceName, &catalogRunsRelease{runtime: runtime}); err != nil {
			fmt.Printf("%s %s: %v\n", label, where, err)
		}
	}
	return place
}

// catalogRuns is the runs database of a name of the host's catalogue: queuestore.Immediate over the app's
// pool of its SQLite file, the pool the app's statements on connection("tên") write it through (openDatabase:
// one per file and app, with the file's process lock), so a write of the runs and a write of the app's tables
// queue on the file's one write lock; the writes of the runs also hold the file's gate (queuestore.FileGate),
// so they go one at a time whatever SQLite's lock does. The tables are made the first time the place is used.
// A write of the running worker (stop set) waits for another writer of the file until stop closes
// (queuestore.Immediate.Done), said on the server's output each time the busy timeout runs out; any other
// write tries once.
type catalogRuns struct {
	tenant *Tenant
	place  *runsPlace
	kind   string
	stop   <-chan struct{}
}

func (c catalogRuns) Read(fn func(db queuestore.SQL) error) error {
	database, err := c.open()
	if err != nil {
		return err
	}
	return database.Read(fn)
}

func (c catalogRuns) Write(fn func(db queuestore.SQL) error) error {
	database, err := c.open()
	if err != nil {
		return err
	}
	c.place.writes.Add(1)
	return database.Write(fn)
}

func (c catalogRuns) open() (queuestore.Immediate, error) {
	config := hostSQLiteConfig(catalogDatabaseName(c.place.name), c.place.path)
	pool := c.tenant.lookupDatabase(config)
	if pool == nil {
		// as the app's statements open it: the folder of a local file is made first
		if dir := filepath.Dir(c.place.path); dir != "." && dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		opened, err := c.tenant.openDatabase(config)
		if err != nil {
			return queuestore.Immediate{}, fmt.Errorf("kitwork_runs của database %s: %w", c.place.name, err)
		}
		pool = opened
	}
	database := queuestore.Immediate{Pool: pool, Gate: queuestore.FileGate(c.place.path), Done: c.stop, Try: catalogRunsTry,
		Waiting: c.waiting, WaitingEvery: catalogRunsWaitingEvery}
	if !c.place.ready.Load() {
		if err := database.Write(queuestore.InitSchema); err != nil {
			return queuestore.Immediate{}, fmt.Errorf("kitwork_runs của database %s: %w", c.place.name, err)
		}
		c.place.ready.Store(true)
	}
	return database, nil
}

// catalogRunsTry is how long one try of the running worker's write waits for another writer of a file of the
// catalogue, so how long it holds the file's gate at most: a request's write, or the app's close, waits that
// long for each worker write ahead of it. catalogRunsWaitingEvery is how often the wait is said.
const (
	catalogRunsTry          = 100 * time.Millisecond
	catalogRunsWaitingEvery = 5 * time.Second
)

// waiting says that a write of the runs waits for another writer of the file, the app's statement on
// connection("tên") or another process.
func (c catalogRuns) waiting(waited time.Duration) {
	label := "[Queue]"
	if c.kind == scheduleKind {
		label = "[Schedule]"
	}
	fmt.Printf("%s kitwork_runs của database %s: một lần ghi chờ khoá ghi của file đã %s, một câu khác đang ghi file\n",
		label, c.place.name, waited.Round(time.Second))
}

// placeName is where the runs of a place live, as a line on the server's output says it.
func placeName(place *runsPlace) string {
	if place == nil {
		return ".database/application/kitwork.db"
	}
	return "database " + place.name
}

// sayLeftBehind says where runs of kind that had not ended stay with no worker reading them: how many the app's
// own database (.database/application/kitwork.db) holds when queue.database or schedule.database put the runs in
// a database of the catalogue, and, whatever the place, that the KitDB file the app's database was before
// 29/10/2026 (application.kitdb) is no longer read until `kitwork migrate kitdb` copies it (kitdb_copy.go). Boot
// does not open that file to count its runs (KROW goes, KITWORK_KITDB.md câu 13.4); once kitwork.db holds the
// mark of the copy nothing is said of it. Neither file is made when it does not exist.
func (t *Tenant) sayLeftBehind(kind string, place *runsPlace, label, call string) {
	noun := "job"
	if kind == scheduleKind {
		noun = "lịch"
	}
	if place != nil {
		count, err := t.appSQLiteRunsLeftBehind(kind)
		switch {
		case err != nil:
			fmt.Printf("%s %s: không đọc được các lần chạy còn ở đó: %v\n", label, placeName(nil), err)
		case count > 0:
			fmt.Printf("%s %s còn %d lần chạy chưa xong của %s (pending, running, cancelling): chúng ở lại đó và không chạy, vì %s đặt các lần chạy ở database %s; đổi database không dời lần chạy\n",
				label, placeName(nil), count, noun, call, place.name)
		}
	}
	legacy, err := t.appKitDBNotCopied()
	switch {
	case err != nil:
		fmt.Printf("%s .database/application/application.kitdb: %v\n", label, err)
	case legacy:
		fmt.Printf("%s .database/application/application.kitdb (KitDB cũ) không còn đọc: các lần chạy của %s nay nằm ở %s; lần chạy chưa xong trong file cũ ở lại đó và không chạy cho tới khi `kitwork migrate kitdb` chép chúng sang .database/application/kitwork.db\n",
			label, noun, placeName(place))
	}
}

// appSQLiteRunsLeftBehind counts the runs of kind the app's own database holds that have not ended, reading
// it without writing: a file that does not exist counts none and is not made, and one without kitwork_runs
// counts none and is not given the table.
func (t *Tenant) appSQLiteRunsLeftBehind(kind string) (int, error) {
	place := t.appRunsPlace()
	if _, err := os.Stat(place.path); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	config := hostSQLiteConfig(catalogDatabaseName(place.name), place.path)
	pool := t.lookupDatabase(config)
	if pool == nil {
		opened, err := t.openDatabase(config)
		if err != nil {
			return 0, err
		}
		pool = opened
	}
	var held int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'kitwork_runs'`).Scan(&held); err != nil || held == 0 {
		return 0, err
	}
	var count int
	err := pool.QueryRow(`SELECT COUNT(*) FROM kitwork_runs WHERE kind = ? AND status IN ('pending', 'running', 'cancelling')`, kind).Scan(&count)
	return count, err
}

// appKitDBNotCopied reports an application.kitdb, the app's database before 29/10/2026, that `kitwork migrate
// kitdb` has not copied (applicationCopied): a file that does not exist is none, and it is never opened.
func (t *Tenant) appKitDBNotCopied() (bool, error) {
	path := t.resolveApp(".database", "application", kitDBApplicationFile)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !applicationCopied(t), nil
}
