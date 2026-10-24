package work

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

// ── loading .shared/queue/ (KITWORK_QUEUE.md mục 2, 3) ─────────────────────────────────────────────
//
// At boot the app's loader runs every file of .shared/queue/ as a module, once, and keeps the jobs each
// exports under the name it exports them: the handlers the worker runs. A job is registered here and only
// here; the same file evaluated in a route's bundle makes handles that send, nothing more.

// queueLegacyLine is what boot says of an app that still has the folder of the old queue.
const queueLegacyLine = "[Queue] _queue/ không còn đọc: khai job trong .shared/queue/<tên>.work bằng export const <tên> = queue.job(fn)"

// queueDestructurable is what a job's handler may take apart of its context: job, never data(), a function.
var queueDestructurable = []string{"job"}

// queueSide says what a job's context offers a name its handler takes apart.
func queueSide(name string) string {
	if name == "data" {
		return "`data` là hàm của `context`; tách ra thì trong JavaScript nó mất `this`: viết `context.data()`"
	}
	return fmt.Sprintf("`%s` không có trong `context` của một job: context có data() và job", name)
}

// queueConfig is what .shared/queue/config.work sets: how many runs one machine runs at once, and how long
// an ended run is kept.
type queueConfig struct {
	parallel int
	keep     time.Duration
}

// LoadQueueFiles reads the app's .shared/queue/ and starts the worker. Every job must be registered before
// a run of it is claimed, so this runs at boot, not on a first request.
func (t *Tenant) LoadQueueFiles() {
	if info, err := os.Stat(t.resolveApp("_queue")); err == nil && info.IsDir() {
		fmt.Println(queueLegacyLine)
	}
	dir := t.resolveApp(".shared", "queue")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no .shared/queue/: the app sends no job
	}
	worker, err := t.ensureQueueWorker()
	if err != nil {
		fmt.Printf("[Queue] worker unavailable: %v\n", err)
		return
	}
	config := t.readQueueConfig(filepath.Join(dir, "config.work"))

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	said := map[string]bool{}
	say := func(line string) {
		if !said[line] {
			said[line] = true
			fmt.Println(line)
		}
	}
	declaredAt := map[string]string{}
	var handlers []*QueueHandler
	// unloaded is what this load could not read: its runs wait for the fix, never fail as runs of no job
	unloaded := queuestore.Withheld{Names: map[string]bool{}, Sources: map[string]bool{}}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			say(fmt.Sprintf("[Queue] .shared/queue/%s/: hàng đợi con (thư mục với config.work riêng) chưa có trong engine hôm nay; job trong đó không chạy", name))
			continue
		}
		if !strings.HasSuffix(name, ".work") || name == "config.work" {
			continue
		}
		relative := ".shared/queue/" + name
		bc, err := compiler.CompileModuleFileInApp(t.resolveApp(), filepath.Join(dir, name))
		if err == nil {
			// a key the file requires that the app's environment has not: the file waits, as one that
			// does not compile (environment.go)
			if missing := t.missingEnvironment(bc); missing != nil {
				err = missing
			}
		}
		if err != nil {
			say(fmt.Sprintf("[Queue] compile %s: %v", relative, err))
			unloaded.Sources[relative] = true
			continue
		}
		loaded, withheld, ran := t.runQueueFile(bc, relative, say)
		if !ran {
			unloaded.Sources[relative] = true
		}
		for _, name := range withheld {
			unloaded.Names[name] = true
		}
		for _, handler := range loaded {
			if first, taken := declaredAt[handler.Name]; taken {
				say(fmt.Sprintf("[Queue] %s: job %s đã khai ở %s; tên job duy nhất trong app", handler.Place, handler.Name, first))
				continue
			}
			declaredAt[handler.Name] = handler.Place
			handlers = append(handlers, handler)
		}
	}
	for name := range declaredAt {
		delete(unloaded.Names, name) // a name another file declares right is that file's job
	}

	worker.mu.Lock()
	worker.handlers = handlers
	worker.byName = make(map[string]*QueueHandler, len(handlers))
	for _, handler := range handlers {
		worker.byName[handler.Name] = handler
	}
	worker.unloaded = unloaded
	worker.parallel, worker.keep = config.parallel, config.keep
	worker.mu.Unlock()
	t.StartQueueWorker()
}

// runQueueFile runs one compiled file of .shared/queue/ in a VM of its own (the tenant's globals copied,
// so its top-level names never reach another file) and gives the jobs it exports. A job with a problem is
// said, and left out: withheld names those of them that have a name. ran is false when the file failed as
// it ran.
func (t *Tenant) runQueueFile(bc *compiler.Bytecode, relative string, say func(string)) (handlers []*QueueHandler, withheld []string, ran bool) {
	declarations := &queueDeclarations{}
	vm := runtime.New(bc.Program)
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(&KitWork{tenant: t, vm: vm, queueJobs: declarations})
	})
	globals := make(map[string]value.Value, len(t.vm.Globals)+1)
	for name, item := range t.vm.Globals {
		globals[name] = item
	}
	globals[kitwork] = kitworkFunc
	vm.Builtins = []value.Value{kitworkFunc}
	vm.Globals = globals
	vm.MaxEnergy = t.MaxEnergy
	defer loadingFile(vm)()
	if result := vm.Run(); result.K == value.Invalid {
		say(fmt.Sprintf("[Queue] run %s: %s", relative, result.Text()))
		return nil, nil, false
	}

	for _, job := range declarations.jobs {
		name, module, retry, timeout, timeoutText, problem := job.declared()
		switch {
		case module != "" && module != relative:
			continue // a job of a file this one imports: loaded with its own file
		case problem != "":
			for _, each := range job.allProblems() {
				say("[Queue] " + each)
			}
			if name != "" {
				withheld = append(withheld, name)
			}
			continue
		case name == "":
			say(fmt.Sprintf("[Queue] %s: queue.job không export: export const camOn = queue.job(…), tên job là tên export; queue.job chỉ khai trong .shared/queue/", job.place))
			continue
		}
		if err := destructureError(job.handler, queueDestructurable, queueSide); err != nil {
			say(fmt.Sprintf("[Queue] %v", err))
			withheld = append(withheld, name)
			continue
		}
		handlers = append(handlers, &QueueHandler{
			Name: name, Source: relative, Place: job.place, Callback: job.handler, Bytecode: bc,
			Retry: retry, Timeout: timeout, TimeoutText: timeoutText, ContentHash: bc.CacheKey(),
		})
	}
	return handlers, withheld, true
}

// readQueueConfig reads .shared/queue/config.work: queue.parallel(n) (runs at once on one machine) and
// queue.keep(duration) (how long an ended run is kept), one call per line (background_config.go). A missing
// file is the defaults; a wrong value is said with where it is written, and its default kept.
func (t *Tenant) readQueueConfig(file string) queueConfig {
	config := queueConfig{parallel: queueDefaultParallel, keep: queueDefaultKeep}
	if _, err := os.Stat(file); err != nil {
		return config
	}
	const where = ".shared/queue/config.work"
	settings := t.runFolderConfig(file, "queue", "[Queue]", where)
	if settings == nil {
		return config
	}
	for _, setting := range settings.calls {
		place := folderPlace(where, setting.place)
		item := setting.only()
		switch setting.word {
		case "parallel":
			if item.K != value.Number || item.N < 1 || item.N > 100 || item.N != math.Trunc(item.N) {
				fmt.Printf("[Queue] %s: queue.parallel nhận một số nguyên từ 1 tới 100, số lần chạy cùng lúc trên một máy: queue.parallel(4)\n", place)
				continue
			}
			config.parallel = int(item.N)
		case "keep":
			duration, err := ParseDuration(item.Text())
			if item.K != value.String || err != nil || duration < time.Hour {
				fmt.Printf("[Queue] %s: queue.keep nhận một khoảng từ \"1h\", thời gian giữ lần chạy đã xong: queue.keep(\"30d\")\n", place)
				continue
			}
			config.keep = duration
		case "database":
			fmt.Printf("[Queue] %s: queue.database(…) chưa có trong engine hôm nay: các lần chạy nằm trong database của app (.database/application/)\n", place)
		}
	}
	return config
}
