package work

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	queuestore "github.com/kitwork/engine/utilities/queue"
	schedulerule "github.com/kitwork/engine/utilities/schedule"
	"github.com/kitwork/engine/value"
)

// ── kitwork/schedule (KITWORK_SCHEDULE.md) ────────────────────────────────────────────────────────
//
// A schedule is declared once, in a file of the app's .schedule/, and exported; its name is its export:
//
//	// app/.schedule/baoCao.work
//	import * as schedule from "kitwork/schedule";
//	export const baoCaoSang = schedule.daily("09:00").retry(3).timeout("10m").run((context) => {
//	  guiBaoCao(context.job.at);
//	});
//
// schedule.<rule>(…) only declares, as queue.job does: every bundle that imports baoCao.work evaluates it
// again, so evaluating it registers nothing and runs nothing. The loader of .schedule/ (schedule_load.go)
// turns what each file exports into the schedules the app's clock runs (schedule_runtime.go); each due time
// is a run stored in kitwork_runs (kind schedule) before it runs, claimed and retried by the worker the queue
// uses. The rule itself, and every error of mục 11, is utilities/schedule's: the compiler reads a chain
// written with literals through it, the runtime reads every chain through it here.

// Schedule is kitwork/schedule: import * as schedule from "kitwork/schedule".
type Schedule struct {
	tenant *Tenant
	vm     *runtime.VM
	// declared is the loader's list of the schedules a file of .schedule/ declares, nil everywhere else.
	declared *scheduleDeclarations
	// settings is what .schedule/config.work calls as the loader runs it (schedule.keep("7d")), nil everywhere
	// else, where such a call is refused (background_config.go).
	settings *folderSettings
}

// scheduleDeclarations is every schedule.<rule>(…) evaluated while the loader runs one file (and the files
// it imports): one VM, one goroutine.
type scheduleDeclarations struct {
	schedules []*scheduleDeclaration
}

// Schedule is the module: a proxy, so the name the bundler gives a schedule (#exports) is no name a program
// reads.
func (w *KitWork) Schedule() value.Value {
	return value.Value{K: value.Proxy, V: &Schedule{tenant: w.tenant, vm: w.vm, declared: w.scheduleDeclarations, settings: w.folderSettings}}
}

func (s *Schedule) OnGet(key string) value.Value {
	if message, removed := schedulerule.RemovedWord(key); removed {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	if schedulerule.RuleWords[key] || schedulerule.ReadWords[key] || compiler.FolderConfigWord("schedule", key) {
		return value.NewFunc(func(args ...value.Value) value.Value { return s.OnInvoke(key, args...) })
	}
	return value.Value{K: value.Nil}
}

func (s *Schedule) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (s *Schedule) OnInvoke(method string, args ...value.Value) value.Value {
	switch {
	case method == compiler.ScheduleExportsMethod:
		return s.exports(args)
	case compiler.FolderConfigWord("schedule", method):
		// schedule.keep("7d") in .schedule/config.work, refused anywhere else
		return s.settings.call(s.vm, "schedule", method, args)
	case schedulerule.ReadWords[method]:
		return (&scheduleQuery{tenant: s.tenant, who: "schedule"}).OnInvoke(method, args...)
	case schedulerule.RuleWords[method] && s.outsideScheduleFolder():
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(compiler.ScheduleRuleOutside, method))
	}
	declaration := &scheduleDeclaration{tenant: s.tenant, vm: s.vm, place: callPlace(s.vm)}
	chain, err := schedulerule.Begin(method, ruleArgs(args))
	if err != nil {
		if _, removed := schedulerule.RemovedWord(method); removed || !schedulerule.RuleWords[method] {
			return value.InvalidFailure(value.InvalidArgumentCode, err.Error())
		}
		declaration.problem(declaration.place, err.Error())
	}
	declaration.chain = chain
	if s.declared != nil {
		s.declared.schedules = append(s.declared.schedules, declaration)
	}
	return value.Value{K: value.Proxy, V: &ScheduleBuilder{declaration: declaration}}
}

// outsideScheduleFolder reports a rule evaluated in a file outside the app's .schedule/ (mục 2), where the
// compiler could not see it: a namespace passed through a function, const s = schedule. The file is the one
// the call is written in as the program's debug table names it, from the folder of the program's entry
// (compiler/bundler.go sourceName): the loader's entry is a file of .schedule/, so a file of .schedule/ is
// named from there; any other program's entry is outside .schedule/ (a route, a job, a config.work), so a
// file of .schedule/ is reached through the app's folder, "../../.schedule/baoCao.work". A call whose file
// the program does not say is let through.
func (s *Schedule) outsideScheduleFolder() bool {
	vm := s.vm
	if vm == nil || vm.Program() == nil || vm.FrameIdx < 0 || vm.FrameIdx >= len(vm.Frames) {
		return false
	}
	file := filepath.ToSlash(vm.Program().SourceAt(vm.Frames[vm.FrameIdx].LastIP).File)
	switch {
	case file == "":
		return false
	case path.Base(file) == "config.work":
		return true
	case s.declared != nil:
		return strings.HasPrefix(file, "../")
	}
	for strings.HasPrefix(file, "../") {
		file = strings.TrimPrefix(file, "../")
	}
	return !strings.HasPrefix(file, ".schedule/")
}

// exports names each schedule of a file of .schedule/ after the key it is exported under, in the order the
// file writes its exports (the bundler's #exports, compiler/schedule.go), and gives the export object back
// as it is.
func (s *Schedule) exports(args []value.Value) value.Value {
	if len(args) != 3 || args[0].K != value.Map || args[2].K != value.Array {
		return value.InvalidFailure(value.InvalidArgumentCode, "schedule.#exports: biên dịch lại file")
	}
	module := args[1].Text()
	exported := args[0].Map()
	for _, item := range args[2].Array() {
		name := item.Text()
		if declaration := declarationOf(exported[name]); declaration != nil {
			declaration.bind(name, module)
		}
	}
	return args[0]
}

// declarationOf is the schedule a value is, a builder or a handle; nil for any other value.
func declarationOf(item value.Value) *scheduleDeclaration {
	if item.K != value.Proxy {
		return nil
	}
	switch held := item.V.(type) {
	case *ScheduleBuilder:
		return held.declaration
	case *ScheduleHandle:
		return held.declaration
	}
	return nil
}

// callPlace is where the call running now in vm is written, file:line:column.
func callPlace(vm *runtime.VM) string {
	if vm == nil || vm.Program() == nil || vm.FrameIdx < 0 || vm.FrameIdx >= len(vm.Frames) {
		return ""
	}
	location := vm.Program().SourceAt(vm.Frames[vm.FrameIdx].LastIP)
	if location.Line <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", path.Base(strings.ReplaceAll(location.File, "\\", "/")), location.Line, location.Column)
}

// ruleArgs are the arguments of a call as the rule reads them.
func ruleArgs(args []value.Value) []schedulerule.Arg {
	out := make([]schedulerule.Arg, len(args))
	for index, arg := range args {
		switch {
		case arg.IsCallable():
			out[index] = schedulerule.Arg{Kind: schedulerule.Function}
		case arg.K == value.String:
			out[index] = schedulerule.Arg{Kind: schedulerule.String, Text: arg.Text()}
		case arg.K == value.Number:
			out[index] = schedulerule.Arg{Kind: schedulerule.Number, Number: arg.N}
		default:
			out[index] = schedulerule.Arg{Kind: schedulerule.Other}
		}
	}
	return out
}

// scheduleDeclaration is one schedule as its file declares it: the chain of its rule, its handler, the
// name it is exported under, and what is wrong with it. A builder and the handle .run(…) or .once(…) gives
// are two faces of it.
type scheduleDeclaration struct {
	tenant *Tenant
	vm     *runtime.VM
	// place is where schedule.<rule>(…) is written.
	place string

	mu       sync.Mutex
	chain    *schedulerule.Chain
	handler  *value.Lambda
	problems []string
	name     string
	module   string
}

func (d *scheduleDeclaration) problem(place, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if place != "" {
		message = place + ": " + message
	}
	d.problems = append(d.problems, message)
}

func (d *scheduleDeclaration) bind(name, module string) {
	d.mu.Lock()
	if d.name == "" {
		d.name, d.module = name, module
		d.mu.Unlock()
		return
	}
	first := d.name
	d.mu.Unlock()
	if first != name {
		d.problem(d.place, fmt.Sprintf("lịch %s export thêm dưới tên %s: một lịch, một tên", first, name))
	}
}

// then chains .word(args): the builder again, or the handle after .run and .once.
func (d *scheduleDeclaration) then(word string, args []value.Value) value.Value {
	place := callPlace(d.vm)
	if place == "" {
		place = d.place
	}
	d.mu.Lock()
	chain := d.chain
	d.mu.Unlock()
	if chain != nil {
		if err := chain.Then(word, ruleArgs(args)); err != nil {
			d.problem(place, err.Error())
		}
	}
	// after .run and .once the handle answers: a second end is its error (ScheduleHandle.OnInvoke), whatever
	// the first end said of its own arguments
	if !schedulerule.IsEnd(word) {
		return value.Value{K: value.Proxy, V: &ScheduleBuilder{declaration: d}}
	}
	if len(args) == 1 && args[0].IsCallable() {
		d.mu.Lock()
		d.handler = lambdaOf(args[0])
		d.mu.Unlock()
	}
	return value.Value{K: value.Proxy, V: &ScheduleHandle{declaration: d}}
}

// declared is the schedule as its file declared it: its name, file, rule (nil when its chain failed or did
// not end), handler, and the first problem ("" when none).
func (d *scheduleDeclaration) declared() (name, module string, rule *schedulerule.Rule, handler *value.Lambda, problem string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.problems) > 0 {
		problem = d.problems[0]
	}
	if d.chain != nil && d.chain.Ended() != "" {
		rule = d.chain.Rule()
	}
	return d.name, d.module, rule, d.handler, problem
}

// allProblems is every problem of the declaration, in the order they were found.
func (d *scheduleDeclaration) allProblems() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.problems...)
}

// usable is nil when the schedule can be read and run now by its name, the error otherwise.
func (d *scheduleDeclaration) usable() (string, value.Value) {
	name, _, rule, _, problem := d.declared()
	switch {
	case problem != "":
		return "", value.InvalidFailure(value.InvalidArgumentCode, problem)
	case name == "":
		return "", value.InvalidFailure(value.InvalidArgumentCode, `lịch chưa có tên: lịch khai trong .schedule/<tên>.work và export, export const baoCaoSang = schedule.daily("09:00").run(fn); file khác import { baoCaoSang } from "app/.schedule/baoCao.work"`)
	case rule == nil:
		return "", value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("lịch %s chưa kết thúc: nối .run(fn), hay .once(fn) cho đúng một lần", name))
	}
	return name, value.Value{K: value.Nil}
}

// ScheduleBuilder is a schedule before .run(…) or .once(…): schedule.daily("09:00").retry(3).
type ScheduleBuilder struct {
	declaration *scheduleDeclaration
}

func (b *ScheduleBuilder) OnGet(key string) value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value { return b.OnInvoke(key, args...) })
}

func (b *ScheduleBuilder) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (b *ScheduleBuilder) OnInvoke(method string, args ...value.Value) value.Value {
	return b.declaration.then(method, args)
}

// ScheduleHandle is what .run(…) and .once(…) give, exported under the schedule's name: it reads the
// schedule's runs and runs it now. Nothing chains after it.
type ScheduleHandle struct {
	declaration *scheduleDeclaration
}

func (h *ScheduleHandle) OnGet(key string) value.Value {
	switch key {
	case "runs", "status", "where", "now", "list":
		return value.NewFunc(func(args ...value.Value) value.Value { return h.OnInvoke(key, args...) })
	}
	if message, removed := schedulerule.RemovedChained(key); removed {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	return value.Value{K: value.Nil}
}

func (h *ScheduleHandle) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (h *ScheduleHandle) OnInvoke(method string, args ...value.Value) value.Value {
	switch method {
	case "runs", "status", "where":
		name, failure := h.declaration.usable()
		if failure.Failed() {
			return failure
		}
		return (&scheduleQuery{tenant: h.declaration.tenant, who: name, name: name}).OnInvoke(method, args...)
	case "list":
		message, _ := schedulerule.RemovedChained("list")
		return value.InvalidFailure(value.InvalidArgumentCode, message+", các lần chạy của lịch bằng .runs()")
	case "now":
		name, failure := h.declaration.usable()
		if failure.Failed() {
			return failure
		}
		if len(args) != 0 {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.now() không nhận tham số: chạy lịch ngay một lần, context.job.at là lúc gọi", name))
		}
		return scheduleNowRun(h.declaration.tenant, name)
	}
	d := h.declaration
	d.mu.Lock()
	ended := ""
	if d.chain != nil {
		ended = d.chain.Ended()
	}
	d.mu.Unlock()
	if ended == "" {
		ended = "run"
	}
	if message, removed := schedulerule.RemovedChained(method); removed {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	if method == "run" || method == "once" {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(".run và .once loại trừ nhau và đứng cuối: lịch đã kết thúc bằng .%s(…), sau đó không nối gì", ended))
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("sau .%s(…) không nối gì: handle của lịch có runs, status, where, now", ended))
}

// ── now() (mục 6) ───────────────────────────────────────────────────────────────────────────────────

// scheduleNowRun stores a run of name due now, outside its rule: context.job.at is when now() was called. The
// app's clock runs it as it runs a due time.
func scheduleNowRun(tenant *Tenant, name string) value.Value {
	worker := tenant.scheduleWorker()
	entry := worker.scheduleEntry(name)
	if entry == nil {
		return value.InvalidFailure(value.NotFoundCode, fmt.Sprintf("%s.now(): lịch %s không chạy trong app (không có trong .schedule/, hay file của nó chưa nạp được)", name, name))
	}
	at := scheduleNow()
	now := time.Now()
	run, _, err := tenant.scheduleStore().Enqueue(queuestore.NewRun{
		Name: name, Data: scheduleRunData(schedulerule.Stamp(at, entry.location), triggerNow),
		MaxAttempts: entry.rule.Retry + 1, Timeout: entry.rule.Timeout, Available: now,
	}, now)
	if err != nil {
		return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("%s.now(): không lưu được lần chạy: %v", name, err))
	}
	wakeWorker(worker, now)
	return value.New(map[string]value.Value{
		"id": value.New(run.ID), "name": value.New(run.Name), "status": value.New(run.Status),
		"at": value.New(schedulerule.Stamp(at, entry.location)),
	})
}

// What started a run of a schedule: its rule (a due time) or now().
const (
	triggerSchedule = "schedule"
	triggerNow      = "now"
)

// scheduleRunData is what a schedule's run keeps in kitwork_runs' data: the due time as the author reads it
// (context.job.at), and what started it. A schedule carries no data of its own (mục 7).
func scheduleRunData(at, trigger string) string {
	encoded, _ := json.Marshal(map[string]string{"at": at, "trigger": trigger})
	return string(encoded)
}

// scheduleRunOf reads what scheduleRunData wrote.
func scheduleRunOf(data string) (at, trigger string) {
	var decoded struct {
		At      string `json:"at"`
		Trigger string `json:"trigger"`
	}
	_ = json.Unmarshal([]byte(data), &decoded)
	return decoded.At, decoded.Trigger
}

// ── the handler's context (mục 7) ───────────────────────────────────────────────────────────────────

// scheduleContext is the context a schedule's handler gets: context.job, the run and this attempt. There is
// no context.data(): a schedule carries no data.
func (t *Tenant) scheduleContext(claim queuestore.Claim, log *queueLog) value.Value {
	at, _ := scheduleRunOf(claim.Data)
	job := value.New(map[string]value.Value{
		"id":             value.New(claim.ID),
		"name":           value.New(claim.Name),
		"attempt":        value.New(claim.Attempt),
		"maxAttempts":    value.New(claim.MaxAttempts),
		"isFinalAttempt": value.New(claim.Attempt >= claim.MaxAttempts),
		"at":             value.New(at),
		"log":            logFunction(log),
	})
	return value.Value{K: value.Proxy, V: &scheduleRunContext{job: job}}
}

// scheduleRunContext is the context of a schedule's run: job, and an error naming the new spelling for each
// word of the old cron's handler argument (scheduledFor, executionId, log, attempt…).
type scheduleRunContext struct {
	job value.Value
}

// scheduleContextRemoved are the words of the old cron's handler argument, by what the spec writes.
var scheduleContextRemoved = map[string]string{
	"scheduledFor":   "context.job.at",
	"executionId":    "context.job.id",
	"log":            "context.job.log(…)",
	"attempt":        "context.job.attempt",
	"maxAttempts":    "context.job.maxAttempts",
	"isFinalAttempt": "context.job.isFinalAttempt",
	"id":             "context.job.id",
	"name":           "context.job.name",
	"at":             "context.job.at",
}

// scheduleNoData is the error of context.data() in a schedule's handler.
const scheduleNoData = `lịch không có context.data(): lịch không mang dữ liệu; việc có dữ liệu là một job, lịch gửi nó bằng camOn.send(data, { key: "…" + context.job.at })`

func (c *scheduleRunContext) OnGet(key string) value.Value {
	switch {
	case key == "job":
		return c.job
	case key == "data":
		return value.InvalidFailure(value.InvalidArgumentCode, scheduleNoData)
	case scheduleContextRemoved[key] != "":
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("context.%s không có ở lịch: %s", key, scheduleContextRemoved[key]))
	}
	return value.Value{K: value.Nil}
}

func (c *scheduleRunContext) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (c *scheduleRunContext) OnInvoke(method string, args ...value.Value) value.Value {
	read := c.OnGet(method)
	if read.Failed() {
		return read
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("context.%s không phải hàm: context của lịch có job", method))
}

// ── reading: list() and runs() (mục 8) ──────────────────────────────────────────────────────────────

// scheduleStatuses are the statuses of a schedule's run (mục 9).
var scheduleStatuses = []string{queuestore.Pending, queuestore.Running, queuestore.Completed, queuestore.Failed}

// scheduleQuery is a read described before its action: schedule.status("failed").runs(10), baoCaoSang.runs().
type scheduleQuery struct {
	tenant *Tenant
	// who is how the read is written, schedule or the schedule's name; name is the schedule it is limited
	// to ("" all).
	who    string
	name   string
	status string
}

func (r *scheduleQuery) OnGet(key string) value.Value {
	switch key {
	case "list", "runs", "status", "where":
		return value.NewFunc(func(args ...value.Value) value.Value { return r.OnInvoke(key, args...) })
	}
	return value.Value{K: value.Nil}
}

func (r *scheduleQuery) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (r *scheduleQuery) OnInvoke(method string, args ...value.Value) value.Value {
	switch method {
	case "status":
		if r.status != "" {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.status(…) viết hai lần: một trạng thái mỗi câu đọc", r.who))
		}
		known := false
		if len(args) == 1 && args[0].K == value.String {
			for _, status := range scheduleStatuses {
				known = known || args[0].Text() == status
			}
		}
		if !known {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.status nhận một trạng thái của lần chạy: %s", r.who, strings.Join(scheduleStatuses, ", ")))
		}
		next := *r
		next.status = args[0].Text()
		return value.Value{K: value.Proxy, V: &next}
	case "where":
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("%s.where(fn) chưa có trong engine hôm nay: điều kiện của where chạy ở tầng lưu trữ, không lọc trong bộ nhớ (KITWORK_SCHEDULE.md mục 8); lọc theo trạng thái là .status(\"failed\")", r.who))
	case "runs":
		return r.runs(args)
	case "list":
		return r.list(args)
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(".%s không có ở đây: câu đọc viết .status(…) rồi .runs(…)", method))
}

// runs reads the runs, newest first.
func (r *scheduleQuery) runs(args []value.Value) value.Value {
	limit, offset, failure := queuePage("runs", args)
	if failure.Failed() {
		return failure
	}
	runs, err := r.tenant.scheduleStore().Runs(queuestore.Filter{Name: r.name, Status: r.status}, limit, offset)
	if err != nil {
		return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("%s.runs: %v", r.who, err))
	}
	out := make([]value.Value, len(runs))
	for index, run := range runs {
		out[index] = scheduleRunRecord(run)
	}
	return value.New(out)
}

// list reads the schedules of the app, by name, whether any is due yet or not: what the app's clock runs.
func (r *scheduleQuery) list(args []value.Value) value.Value {
	if r.name != "" {
		message, _ := schedulerule.RemovedChained("list")
		return value.InvalidFailure(value.InvalidArgumentCode, message+", các lần chạy của lịch bằng .runs()")
	}
	if r.status != "" {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`schedule.status("%s").list() không có: trạng thái chỉ có ở lần chạy, viết schedule.status("%s").runs()`, r.status, r.status))
	}
	limit, offset, failure := queuePage("list", args)
	if failure.Failed() {
		return failure
	}
	listed := r.tenant.scheduleWorker().listSchedules(scheduleNow())
	sort.Slice(listed, func(i, j int) bool { return listed[i]["name"].Text() < listed[j]["name"].Text() })
	if offset > len(listed) {
		offset = len(listed)
	}
	listed = listed[offset:]
	if len(listed) > limit {
		listed = listed[:limit]
	}
	out := make([]value.Value, len(listed))
	for index, item := range listed {
		out[index] = value.New(item)
	}
	return value.New(out)
}

// scheduleRunRecord is a run as runs() gives it: id, the schedule's name, the due time (at) and what started
// it (trigger: schedule or now), status, attempts, log, error, and its times.
func scheduleRunRecord(run queuestore.Run) value.Value {
	text := func(s string) value.Value {
		if s == "" {
			return value.Value{K: value.Nil}
		}
		return value.New(s)
	}
	at, trigger := scheduleRunOf(run.Data)
	record := map[string]value.Value{
		"id":          value.New(run.ID),
		"name":        value.New(run.Name),
		"at":          text(at),
		"trigger":     text(trigger),
		"status":      value.New(run.Status),
		"attempt":     value.New(run.Attempt),
		"maxAttempts": value.New(run.MaxAttempts),
		"log":         value.New(run.Log),
		"error":       value.Value{K: value.Nil},
		"createdAt":   value.New(run.CreatedAt),
		"startedAt":   text(run.StartedAt),
		"finishedAt":  text(run.FinishedAt),
		"nextRunAt":   value.Value{K: value.Nil},
	}
	if run.ErrorCode != "" || run.ErrorMessage != "" {
		record["error"] = value.New(map[string]value.Value{"code": value.New(run.ErrorCode), "message": value.New(run.ErrorMessage)})
	}
	if run.Status == queuestore.Pending {
		record["nextRunAt"] = value.New(run.AvailableAt)
	}
	return value.New(record)
}
