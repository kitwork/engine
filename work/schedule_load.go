package work

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	queuestore "github.com/kitwork/engine/utilities/queue"
	schedulerule "github.com/kitwork/engine/utilities/schedule"
	"github.com/kitwork/engine/value"
)

// ── loading .schedule/ (KITWORK_SCHEDULE.md mục 2, 6) ─────────────────────────────────────────────
//
// At boot the app's loader runs every file of .schedule/ as a module, once, and keeps the schedules each
// exports under the name it exports them: what the app's clock runs. A schedule is registered here and
// only here; the same file evaluated in a route's bundle makes handles that read and run now, nothing more.
// A schedule with a problem (mục 11) is said on the server's output with its file:line:column and left out;
// the others run.

// scheduleLegacyLine is what boot says of an app that still has the folder of the old cron.
const scheduleLegacyLine = "[Schedule] _cron/ không còn đọc: lịch ở .schedule/<tên>.work, export const <tên> = schedule.daily(\"09:00\").run(fn), tên lịch là tên export"

// scheduleDestructurable is what a schedule's handler may take apart of its context: job.
var scheduleDestructurable = []string{"job"}

// scheduleSide says what a schedule's context offers a name its handler takes apart.
func scheduleSide(name string) string {
	if name == "data" {
		return scheduleNoData
	}
	return fmt.Sprintf("`%s` không có trong `context` của một lịch: context có job", name)
}

// scheduleConfig is what .schedule/config.work sets: how long an ended run is kept.
type scheduleConfig struct {
	keep time.Duration
}

// LoadScheduleFiles reads the app's .schedule/ and starts its clock. Every schedule must be registered
// before its first due time, so this runs at boot, not on a first request.
func (t *Tenant) LoadScheduleFiles() {
	if info, err := os.Stat(t.resolveApp("_cron")); err == nil && info.IsDir() {
		fmt.Println(scheduleLegacyLine)
	}
	dir := t.resolveApp(".schedule")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no .schedule/: the app has no schedule
	}
	worker, err := t.ensureScheduleWorker()
	if err != nil {
		fmt.Printf("[Schedule] clock unavailable: %v\n", err)
		return
	}
	config := t.readScheduleConfig(filepath.Join(dir, "config.work"))

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	said := map[string]bool{}
	say := func(line string) {
		if !said[line] {
			said[line] = true
			fmt.Println(line)
		}
	}
	declaredAt := map[string]string{}
	var loaded []*scheduleEntry
	handlers := map[string]*QueueHandler{}
	// unloaded is what this load could not read: its runs wait for the fix, never fail as runs of no schedule
	unloaded := queuestore.Withheld{Names: map[string]bool{}, Sources: map[string]bool{}}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			say(fmt.Sprintf("[Schedule] .schedule/%s/: thư mục con trong .schedule/ không đọc; lịch đặt thẳng trong .schedule/", name))
			continue
		}
		if !strings.HasSuffix(name, ".work") || name == "config.work" {
			continue
		}
		relative := ".schedule/" + name
		bc, err := compiler.CompileModuleFileInApp(t.resolveApp(), filepath.Join(dir, name))
		if err == nil {
			// a key the file requires that the app's environment has not: the file waits, as one that
			// does not compile (environment.go)
			if missing := t.missingEnvironment(bc); missing != nil {
				err = missing
			}
		}
		if err != nil {
			say(fmt.Sprintf("[Schedule] compile %s: %v", relative, err))
			unloaded.Sources[relative] = true
			continue
		}
		schedules, withheld, ran := t.runScheduleFile(bc, relative, say)
		if !ran {
			unloaded.Sources[relative] = true
		}
		for _, name := range withheld {
			unloaded.Names[name] = true
		}
		for _, schedule := range schedules {
			if first, taken := declaredAt[schedule.entry.name]; taken {
				say(fmt.Sprintf("[Schedule] %s: lịch %s đã khai ở %s; tên lịch duy nhất trong app", schedule.entry.place, schedule.entry.name, first))
				continue
			}
			declaredAt[schedule.entry.name] = schedule.entry.place
			loaded = append(loaded, schedule.entry)
			handlers[schedule.entry.name] = schedule.handler
		}
	}
	for name := range declaredAt {
		delete(unloaded.Names, name) // a name another file declares right is that file's schedule
	}
	loaded = t.refusePastOnce(loaded, say)
	for _, entry := range loaded {
		// mục 5 counts a schedule without .timezone(…) in router.timezone(…) of the app's config.work, which
		// the engine does not read yet: said, so a schedule written as the spec writes it does not fire at an
		// hour of UTC unseen
		if entry.rule.Zone == "" && entry.rule.Kind != schedulerule.On {
			say(fmt.Sprintf("[Schedule] %s: lịch %s không viết .timezone(…): đếm theo UTC, vì engine chưa đọc router.timezone(…) của config.work gốc app; viết .timezone(\"Asia/Ho_Chi_Minh\") cho lịch", entry.place, entry.name))
		}
	}

	worker.mu.Lock()
	worker.handlers = worker.handlers[:0]
	worker.byName = make(map[string]*QueueHandler, len(loaded))
	worker.book.entries = loaded
	worker.book.byName = make(map[string]*scheduleEntry, len(loaded))
	for _, entry := range loaded {
		worker.handlers = append(worker.handlers, handlers[entry.name])
		worker.byName[entry.name] = handlers[entry.name]
		worker.book.byName[entry.name] = entry
	}
	worker.unloaded = unloaded
	worker.keep = config.keep
	worker.mu.Unlock()
	t.StartScheduleWorker()
}

// loadedSchedule is a schedule a file exports: what the clock reads, and the handler the worker runs.
type loadedSchedule struct {
	entry   *scheduleEntry
	handler *QueueHandler
}

// runScheduleFile runs one compiled file of .schedule/ in a VM of its own (the tenant's globals copied, so
// its top-level names never reach another file) and gives the schedules it exports. A schedule with a
// problem is said, and left out: withheld names those of them that have a name. ran is false when the file
// failed as it ran.
func (t *Tenant) runScheduleFile(bc *compiler.Bytecode, relative string, say func(string)) (schedules []loadedSchedule, withheld []string, ran bool) {
	declarations := &scheduleDeclarations{}
	vm := runtime.New(bc.Program)
	kitworkFunc := value.NewFunc(func(args ...value.Value) value.Value {
		return value.New(&KitWork{tenant: t, vm: vm, scheduleDeclarations: declarations})
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
		say(fmt.Sprintf("[Schedule] run %s: %s", relative, result.Text()))
		return nil, nil, false
	}

	for _, declaration := range declarations.schedules {
		name, module, rule, handler, problem := declaration.declared()
		switch {
		case module != "" && module != relative:
			continue // a schedule of a file this one imports: loaded with its own file
		case problem != "":
			for _, each := range declaration.allProblems() {
				say("[Schedule] " + each)
			}
			if name != "" {
				withheld = append(withheld, name)
			}
			continue
		case name == "":
			say(fmt.Sprintf("[Schedule] %s: lịch không export: export const baoCaoSang = schedule.daily(\"09:00\").run(fn), tên lịch là tên export; lịch không tên thì lịch sử lạc mỗi lần sửa", declaration.place))
			continue
		case rule == nil:
			say(fmt.Sprintf("[Schedule] %s: lịch %s chưa kết thúc: nối .run(fn), hay .once(fn) cho đúng một lần", declaration.place, name))
			withheld = append(withheld, name)
			continue
		}
		if err := destructureError(handler, scheduleDestructurable, scheduleSide); err != nil {
			say(fmt.Sprintf("[Schedule] %v", err))
			withheld = append(withheld, name)
			continue
		}
		location := rule.Location
		switch {
		case location != nil:
		case rule.Kind == schedulerule.On:
			// an instant carries its own offset ("+07:00"): its due time reads as it was written
			location = rule.Instant.Location()
		default:
			location = scheduleDefaultLocation
		}
		// the rule as kitwork_schedules keeps it: another rule, or another zone, is a schedule counted anew
		state := rule.Mode() + " " + rule.Text() + " " + location.String()
		schedules = append(schedules, loadedSchedule{
			entry: &scheduleEntry{name: name, source: relative, place: declaration.place, rule: rule,
				location: location, state: state},
			handler: &QueueHandler{
				Name: name, Source: relative, Place: declaration.place, Callback: handler, Bytecode: bc,
				Retry: rule.Retry, Timeout: rule.Timeout, TimeoutText: rule.TimeoutText, ContentHash: bc.CacheKey(),
			},
		})
		rule.Location = location
	}
	return schedules, withheld, true
}

// refusePastOnce leaves out a schedule.on(…) whose instant has passed (mục 3: "thời điểm đã qua lúc khai là
// lỗi boot"), unless kitwork_schedules holds it with the same rule: it was declared before its instant, and
// has run, or runs now as a due time missed.
func (t *Tenant) refusePastOnce(entries []*scheduleEntry, say func(string)) []*scheduleEntry {
	now := scheduleNow()
	var states map[string]queuestore.ScheduleState
	kept := entries[:0]
	for _, entry := range entries {
		if entry.rule.Kind != schedulerule.On || entry.rule.Instant.After(now) {
			kept = append(kept, entry)
			continue
		}
		if states == nil {
			read, err := t.scheduleStore().ScheduleStates()
			if err != nil {
				say(fmt.Sprintf("[Schedule] kitwork_schedules: %v", err))
				read = map[string]queuestore.ScheduleState{}
			}
			states = read
		}
		if state, declared := states[entry.name]; declared && state.Rule == entry.state {
			kept = append(kept, entry)
			continue
		}
		say(fmt.Sprintf("[Schedule] %s: schedule.on(%q) đã qua lúc khai: thời điểm của once() phải ở tương lai", entry.place, entry.rule.InstantText))
	}
	return kept
}

// readScheduleConfig reads .schedule/config.work: schedule.keep(duration) (how long an ended run is kept),
// one call per line (background_config.go); schedule.session({ … }) and schedule.database(db) are said, the
// engine has neither yet. A missing file is the defaults; a wrong value is said with where it is written, and
// its default kept.
func (t *Tenant) readScheduleConfig(file string) scheduleConfig {
	config := scheduleConfig{keep: queueDefaultKeep}
	if _, err := os.Stat(file); err != nil {
		return config
	}
	const where = ".schedule/config.work"
	settings := t.runFolderConfig(file, "schedule", "[Schedule]", where)
	if settings == nil {
		return config
	}
	for _, setting := range settings.calls {
		place := folderPlace(where, setting.place)
		item := setting.only()
		switch setting.word {
		case "keep":
			duration, err := ParseDuration(item.Text())
			if item.K != value.String || err != nil || duration < time.Hour {
				fmt.Printf("[Schedule] %s: schedule.keep nhận một khoảng từ \"1h\", thời gian giữ lần chạy đã xong: schedule.keep(\"7d\")\n", place)
				continue
			}
			config.keep = duration
		case "session":
			if item.K != value.Map {
				fmt.Printf("[Schedule] %s: schedule.session nhận một object, danh tính dịch vụ của lịch: schedule.session({ role: \"system\" })\n", place)
				continue
			}
			fmt.Printf("[Schedule] %s: schedule.session(…) chưa có trong engine hôm nay: lịch chạy không phiên\n", place)
		case "database":
			fmt.Printf("[Schedule] %s: schedule.database(…) chưa có trong engine hôm nay: các lần chạy nằm trong database của app (.database/application/)\n", place)
		}
	}
	return config
}
