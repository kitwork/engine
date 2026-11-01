package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	queuestore "github.com/kitwork/engine/utilities/queue"
	"github.com/kitwork/engine/value"
)

// ── kitwork/queue (KITWORK_QUEUE.md) ──────────────────────────────────────────────────────────────
//
// A job is declared once, in a file of the app's .shared/queue/, and exported; whoever needs it imports
// the handle and sends through it:
//
//	// app/.shared/queue/camOn.work
//	import * as queue from "kitwork/queue";
//	export const camOn = queue.job((context) => {
//	  const { email, orderId } = context.data();
//	  sendOrderEmail(email, orderId);
//	}).retry(3).timeout("30s");
//
//	// a route
//	import { camOn } from "app/.shared/queue/camOn.work";
//	camOn.send({ email, orderId }, { delay: "5m", key: "order:" + orderId });   // { id, name: "camOn", status: "pending" }
//
// The job's name is its export (compiler/queue.go names the handle on its way out of the file). queue.job
// only declares: it registers nothing and runs nothing, wherever it is evaluated, because every bundle that
// imports camOn.work evaluates it again. Only the loader of .shared/queue/ (LoadQueueFiles) turns what a
// file exports into the handlers the worker runs. send stores the run in kitwork_runs of the app's own
// database (queue_runs.go) before it answers; the worker (queue_persist.go) claims it with a lease.

const (
	// queueDataLimit is the most a run's data may be, written as JSON (mục 4: "có trần kích thước").
	queueDataLimit = 64 << 10
	// queueRunsDefault and queueRunsMaximum bound runs() and list(): how many when the call does not say,
	// and the most one call reads.
	queueRunsDefault = 50
	queueRunsMaximum = 1000
	// queueTimeoutMaximum is the longest .timeout() of an attempt.
	queueTimeoutMaximum = 24 * time.Hour
)

// Queue is kitwork/queue: import * as queue from "kitwork/queue".
type Queue struct {
	tenant *Tenant
	vm     *runtime.VM
	// declared is the loader's list of the jobs a file of .shared/queue/ declares, nil everywhere else.
	declared *queueDeclarations
	// settings is what .shared/queue/config.work calls as the loader runs it (queue.parallel(4)), nil
	// everywhere else, where such a call is refused (background_config.go).
	settings *folderSettings
}

// queueDeclarations is every queue.job(…) evaluated while the loader runs one file (and the files it
// imports): one VM, one goroutine.
type queueDeclarations struct {
	jobs []*QueueJob
}

// Queue is the module: a proxy, so the name the bundler gives a job (#exports) is no name a program reads.
func (w *KitWork) Queue() value.Value {
	return value.Value{K: value.Proxy, V: &Queue{tenant: w.tenant, vm: w.vm, declared: w.queueJobs, settings: w.folderSettings}}
}

// queueWords are the words of the module, what queue.<word>(…) calls.
var queueWords = map[string]bool{"job": true, "list": true, "runs": true, "status": true, "where": true, "cancel": true, "replay": true}

// The words of the module and of a handle the spec replaced are compile errors where the compiler sees
// them (compiler/queue.go); a read or a call of one the compiler could not see (a namespace passed
// around) is the same error when it runs.

func (q *Queue) OnGet(key string) value.Value {
	if message, removed := compiler.QueueRemovedWord(key); removed {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	if queueWords[key] || compiler.FolderConfigWord("queue", key) {
		return value.NewFunc(func(args ...value.Value) value.Value { return q.OnInvoke(key, args...) })
	}
	return value.Value{K: value.Nil}
}

func (q *Queue) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (q *Queue) OnInvoke(method string, args ...value.Value) value.Value {
	if compiler.FolderConfigWord("queue", method) {
		// queue.parallel(4) in .shared/queue/config.work, refused anywhere else
		return q.settings.call(q.vm, "queue", method, args)
	}
	switch method {
	case "job":
		return q.job(args)
	case "list", "runs", "status", "where":
		return (&queueQuery{tenant: q.tenant, who: "queue"}).OnInvoke(method, args...)
	case "cancel":
		return queueCancel(q.tenant, "queue", "", args)
	case "replay":
		return queueReplay(q.tenant, "queue", "", args)
	case compiler.QueueExportsMethod:
		return q.exports(args)
	}
	if message, removed := compiler.QueueRemovedWord(method); removed {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("queue.%s không có: kitwork/queue có job, list, runs, status, where, cancel, replay; .shared/queue/config.work gọi parallel, keep, database", method))
}

// place is where the call running now is written, file:line:column.
func (q *Queue) place() string {
	vm := q.vm
	if vm == nil || vm.Program() == nil || vm.FrameIdx < 0 || vm.FrameIdx >= len(vm.Frames) {
		return ""
	}
	location := vm.Program().SourceAt(vm.Frames[vm.FrameIdx].LastIP)
	if location.Line <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", path.Base(strings.ReplaceAll(location.File, "\\", "/")), location.Line, location.Column)
}

// job is queue.job(handler): the handle, before its rules. It runs nothing and registers nothing.
func (q *Queue) job(args []value.Value) value.Value {
	job := &QueueJob{tenant: q.tenant, place: q.place()}
	if len(args) == 1 && args[0].IsCallable() {
		job.handler = lambdaOf(args[0])
	}
	if job.handler == nil {
		job.problem("queue.job nhận một hàm: queue.job((context) => { … })")
	} else if job.place == "" {
		job.place = lambdaPlace(job.handler)
	}
	if q.declared != nil {
		q.declared.jobs = append(q.declared.jobs, job)
	}
	return value.Value{K: value.Proxy, V: job}
}

// exports names each job of a file of .shared/queue/ after the key it is exported under, in the order the
// file writes its exports (the bundler's #exports, compiler/queue.go), and gives the export object back as
// it is.
func (q *Queue) exports(args []value.Value) value.Value {
	if len(args) != 3 || args[0].K != value.Map || args[2].K != value.Array {
		return value.InvalidFailure(value.InvalidArgumentCode, "queue.#exports: biên dịch lại file")
	}
	module := args[1].Text()
	exported := args[0].Map()
	for _, item := range args[2].Array() {
		name := item.Text()
		if job, ok := exported[name].V.(*QueueJob); ok && exported[name].K == value.Proxy {
			job.bind(name, module)
		}
	}
	return args[0]
}

// QueueJob is the handle queue.job(fn) gives: the handler, the rules chained after it, and the name it is
// exported under. Evaluating it again (every importer does) makes another handle of the same job.
type QueueJob struct {
	tenant  *Tenant
	place   string
	handler *value.Lambda

	mu          sync.Mutex
	retry       int
	retrySet    bool
	timeout     time.Duration
	timeoutText string
	timeoutSet  bool
	problems    []string
	name        string
	module      string
}

func (j *QueueJob) problem(message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.place != "" {
		message = j.place + ": " + message
	}
	j.problems = append(j.problems, message)
}

func (j *QueueJob) bind(name, module string) {
	j.mu.Lock()
	if j.name == "" {
		j.name, j.module = name, module
		j.mu.Unlock()
		return
	}
	first := j.name
	j.mu.Unlock()
	if first != name {
		j.problem(fmt.Sprintf("job %s export thêm dưới tên %s: một job, một tên", first, name))
	}
}

// declared is the job as its file declared it: name, file, rules, and the first problem ("" when none).
func (j *QueueJob) declared() (name, module string, retry int, timeout time.Duration, timeoutText, problem string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.problems) > 0 {
		problem = j.problems[0]
	}
	return j.name, j.module, j.retry, j.timeout, j.timeoutText, problem
}

// allProblems is every problem of the declaration, in the order they were found.
func (j *QueueJob) allProblems() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.problems...)
}

func (j *QueueJob) OnGet(key string) value.Value {
	if message, removed := compiler.QueueJobRemovedWord(key); removed {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	switch key {
	case "retry", "timeout", "send", "runs", "status", "where", "cancel", "replay":
		return value.NewFunc(func(args ...value.Value) value.Value { return j.OnInvoke(key, args...) })
	}
	return value.Value{K: value.Nil}
}

func (j *QueueJob) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (j *QueueJob) OnInvoke(method string, args ...value.Value) value.Value {
	switch method {
	case "retry":
		return j.setRetry(args)
	case "timeout":
		return j.setTimeout(args)
	case "send":
		return j.send(args)
	case "runs", "status", "where":
		name, _, _, _, _, problem := j.declared()
		if failure := jobUsable(name, problem); failure.Failed() {
			return failure
		}
		return (&queueQuery{tenant: j.tenant, who: name, job: name}).OnInvoke(method, args...)
	case "cancel", "replay":
		name, _, _, _, _, problem := j.declared()
		if failure := jobUsable(name, problem); failure.Failed() {
			return failure
		}
		if method == "cancel" {
			return queueCancel(j.tenant, name, name, args)
		}
		return queueReplay(j.tenant, name, name, args)
	}
	if message, removed := compiler.QueueJobRemovedWord(method); removed {
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(".%s không có trên job: queue.job(fn) nối .retry(n), .timeout(hạn); handle có send, runs, status, where, cancel, replay", method))
}

func jobUsable(name, problem string) value.Value {
	if problem != "" {
		return value.InvalidFailure(value.InvalidArgumentCode, problem)
	}
	if name == "" {
		return value.InvalidFailure(value.InvalidArgumentCode, `job chưa có tên: queue.job khai trong .shared/queue/<tên>.work và export, export const camOn = queue.job(fn); file khác import { camOn } from "app/.shared/queue/camOn.work" rồi camOn.send(data)`)
	}
	return value.Value{K: value.Nil}
}

// setRetry is .retry(n): n attempts more after the first, .retry(3) four in all (mục 3).
func (j *QueueJob) setRetry(args []value.Value) value.Value {
	self := value.Value{K: value.Proxy, V: j}
	j.mu.Lock()
	again := j.retrySet
	j.retrySet = true
	j.mu.Unlock()
	if again {
		j.problem(".retry nối hai lần: một job một luật, .retry(3)")
		return self
	}
	if len(args) != 1 || args[0].K != value.Number || args[0].N < 0 || args[0].N != math.Trunc(args[0].N) || args[0].N > 100 {
		j.problem(".retry(n) nhận số lần thử thêm, số nguyên từ 0 tới 100: .retry(3) là bốn lần tổng cộng")
		return self
	}
	j.mu.Lock()
	j.retry = int(args[0].N)
	j.mu.Unlock()
	return self
}

// setTimeout is .timeout(hạn): the longest one attempt may run, "30s", "5m".
func (j *QueueJob) setTimeout(args []value.Value) value.Value {
	self := value.Value{K: value.Proxy, V: j}
	j.mu.Lock()
	again := j.timeoutSet
	j.timeoutSet = true
	j.mu.Unlock()
	if again {
		j.problem(".timeout nối hai lần: một job một luật, .timeout(\"30s\")")
		return self
	}
	if len(args) != 1 || args[0].K != value.String {
		j.problem(".timeout(hạn) nhận một khoảng viết bằng chuỗi: .timeout(\"30s\"), \"5m\", \"1h\"")
		return self
	}
	text := strings.TrimSpace(args[0].Text())
	duration, err := ParseDuration(text)
	if err != nil || duration <= 0 || duration > queueTimeoutMaximum {
		j.problem(fmt.Sprintf(".timeout(%q): hạn của một lần thử là một khoảng từ 1ms tới 24h: \"30s\", \"5m\", \"1h\"", text))
		return self
	}
	j.mu.Lock()
	j.timeout, j.timeoutText = duration, text
	j.mu.Unlock()
	return self
}

// send stores one run of the job and answers its receipt (mục 4). The run is stored before the answer;
// the handler never runs in the caller's call.
func (j *QueueJob) send(args []value.Value) value.Value {
	name, _, retry, timeout, _, problem := j.declared()
	if failure := jobUsable(name, problem); failure.Failed() {
		return failure
	}
	if len(args) > 2 {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.send nhận dữ liệu và một object lựa chọn: %s.send(data, { delay, key })", name, name))
	}
	data := "null"
	if len(args) > 0 {
		plain, walked, err := jobData(args[0])
		if walked != nil {
			return walked.Failure(name + ".send")
		}
		if err != nil {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.send: dữ liệu của job phải viết được thành JSON: %v", name, err))
		}
		encoded, err := json.Marshal(plain)
		if err != nil {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.send: dữ liệu của job phải viết được thành JSON: %v", name, err))
		}
		data = string(encoded)
	}
	if len(data) > queueDataLimit {
		return value.InvalidFailure(value.TooLargeCode, fmt.Sprintf("%s.send: dữ liệu %d byte, quá trần %d KiB của một lần chạy; lưu phần lớn vào database rồi gửi id", name, len(data), queueDataLimit>>10))
	}
	var delay time.Duration
	key := ""
	if len(args) > 1 {
		var failure value.Value
		if delay, key, failure = sendOptions(name, args[1]); failure.Failed() {
			return failure
		}
	}
	now := time.Now()
	run, duplicate, err := j.tenant.queueStore().Enqueue(queuestore.NewRun{
		Name: name, Data: data, Key: key, MaxAttempts: retry + 1, Timeout: timeout, Available: now.Add(delay),
	}, now)
	if err != nil {
		return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("%s.send: không lưu được lần chạy: %v", name, err))
	}
	if !duplicate {
		j.tenant.queueRunDue(now.Add(delay))
	}
	receipt := map[string]value.Value{
		"id":     value.New(run.ID),
		"name":   value.New(run.Name),
		"status": value.New(run.Status),
	}
	if duplicate {
		receipt["duplicate"] = value.New(true)
	}
	return value.New(receipt)
}

// sendOptions reads send's second argument, { delay, key } and nothing else.
func sendOptions(name string, options value.Value) (time.Duration, string, value.Value) {
	usage := fmt.Sprintf(`%s.send(data, { delay: "5m", key: "order:123" })`, name)
	if options.K == value.Nil {
		return 0, "", value.Value{K: value.Nil}
	}
	if options.K != value.Map {
		return 0, "", value.InvalidFailure(value.InvalidArgumentCode, "lựa chọn của send là một object: "+usage)
	}
	var delay time.Duration
	key := ""
	entries := options.Map()
	keys := make([]string, 0, len(entries))
	for option := range entries {
		keys = append(keys, option)
	}
	sort.Strings(keys)
	for _, option := range keys {
		item := entries[option]
		switch option {
		case "delay":
			if item.K != value.String {
				return 0, "", value.InvalidFailure(value.InvalidArgumentCode, "delay là một khoảng viết bằng chuỗi: "+usage)
			}
			parsed, err := ParseDuration(item.Text())
			if err != nil || parsed <= 0 {
				return 0, "", value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("delay %q không đọc được: một khoảng như \"30s\", \"5m\", \"1h\"", item.Text()))
			}
			delay = parsed
		case "key":
			if item.K != value.String || strings.TrimSpace(item.Text()) == "" {
				return 0, "", value.InvalidFailure(value.InvalidArgumentCode, "key là một chuỗi không rỗng: "+usage)
			}
			key = item.Text()
		default:
			return 0, "", value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("send không có lựa chọn %s: chỉ delay và key, %s", option, usage))
		}
	}
	return delay, key, value.Value{K: value.Nil}
}

// jobData is a value as plain data JSON writes: null, booleans, finite numbers, strings, arrays and objects
// of them. A function, a handle, a connection or a request is refused, never written as {}. The walk keeps
// its path (value.Path): data that holds itself, or nests past value.MaxDepth, ends in the *WalkError, never
// in Go's stack, which would take the host down with every app on it.
func jobData(item value.Value) (any, *value.WalkError, error) {
	walker := &jobDataWalk{steps: []string{"data"}}
	walker.path.Root(item)
	plain, err := walker.plain(item)
	return plain, walker.walked, err
}

// jobDataWalk is one walk of jobData: the path it is on, and the names of the steps to it, joined into
// data.items[2].email only when an error says where.
type jobDataWalk struct {
	path   value.Path
	steps  []string
	walked *value.WalkError
}

func (w *jobDataWalk) at() string { return strings.Join(w.steps, "") }

// enter puts item, held under key or at index, on the path; false when the walk ends there.
func (w *jobDataWalk) enter(item value.Value, key string, index int) bool {
	if item.K != value.Map && item.K != value.Array {
		return true
	}
	var walked *value.WalkError
	if index < 0 {
		walked = w.path.Key(item, key)
	} else {
		walked = w.path.Index(item, index)
	}
	if walked != nil {
		w.walked = walked
		return false
	}
	return true
}

func (w *jobDataWalk) leave(item value.Value) {
	if item.K == value.Map || item.K == value.Array {
		w.path.Leave()
	}
}

func (w *jobDataWalk) plain(item value.Value) (any, error) {
	switch item.K {
	case value.Nil:
		return nil, nil
	case value.Bool:
		return item.IsTrue(), nil
	case value.Number:
		if math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return nil, fmt.Errorf("%s là %v, JSON không có số này", w.at(), item.N)
		}
		return item.N, nil
	case value.String, value.Time:
		return item.Text(), nil
	case value.Array:
		items := item.Array()
		out := make([]any, len(items))
		for index, element := range items {
			if !w.enter(element, "", index) {
				return nil, nil
			}
			w.steps = append(w.steps, "["+strconv.Itoa(index)+"]")
			plain, err := w.plain(element)
			w.steps = w.steps[:len(w.steps)-1]
			w.leave(element)
			if err != nil || w.walked != nil {
				return nil, err
			}
			out[index] = plain
		}
		return out, nil
	case value.Map:
		out := map[string]any{}
		for key, element := range item.Map() {
			if !w.enter(element, key, -1) {
				return nil, nil
			}
			w.steps = append(w.steps, "."+key)
			plain, err := w.plain(element)
			w.steps = w.steps[:len(w.steps)-1]
			w.leave(element)
			if err != nil || w.walked != nil {
				return nil, err
			}
			out[key] = plain
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s là %s, không phải dữ liệu", w.at(), item.K)
}

// ── reading: list() and runs() (mục 8) ──────────────────────────────────────────────────────────────

// queueQuery is a read described before its action: queue.status("failed").runs(10), camOn.runs().
type queueQuery struct {
	tenant *Tenant
	// who is how the read is written, queue or the job's name; job is the job it is limited to ("" all).
	who    string
	job    string
	status string
}

func (r *queueQuery) OnGet(key string) value.Value {
	switch key {
	case "list", "runs", "status", "where":
		return value.NewFunc(func(args ...value.Value) value.Value { return r.OnInvoke(key, args...) })
	}
	return value.Value{K: value.Nil}
}

func (r *queueQuery) OnCompare(_ string, _ value.Value) value.Value { return value.NewNil() }

func (r *queueQuery) OnInvoke(method string, args ...value.Value) value.Value {
	switch method {
	case "status":
		if r.status != "" {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.status(…) viết hai lần: một trạng thái mỗi câu đọc", r.who))
		}
		if len(args) != 1 || args[0].K != value.String || !knownStatus(args[0].Text()) {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s.status nhận một trạng thái của lần chạy: %s", r.who, strings.Join(queuestore.Statuses, ", ")))
		}
		next := *r
		next.status = args[0].Text()
		return value.Value{K: value.Proxy, V: &next}
	case "where":
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("%s.where(fn) chưa có trong engine hôm nay: điều kiện của where chạy ở tầng lưu trữ, không lọc trong bộ nhớ (KITWORK_QUEUE.md mục 8); lọc theo trạng thái là .status(\"failed\")", r.who))
	case "runs":
		return r.runs(args)
	case "list":
		return r.list(args)
	}
	return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(".%s không có ở đây: câu đọc viết .status(…) rồi .runs(…)", method))
}

func knownStatus(status string) bool {
	for _, known := range queuestore.Statuses {
		if status == known {
			return true
		}
	}
	return false
}

// queuePage reads the argument of list() and runs(): nothing, a number (how many), or { page, limit }
// with pages from 1.
func queuePage(word string, args []value.Value) (int, int, value.Value) {
	usage := fmt.Sprintf("%s() nhận không gì, một số (bao nhiêu), hay { page, limit }: %s(10), %s({ page: 2, limit: 20 })", word, word, word)
	whole := func(item value.Value) bool {
		return item.K == value.Number && item.N >= 1 && item.N == math.Trunc(item.N)
	}
	switch {
	case len(args) == 0:
		return queueRunsDefault, 0, value.Value{K: value.Nil}
	case len(args) > 1:
		return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, usage)
	case args[0].K == value.Number:
		if !whole(args[0]) || args[0].N > queueRunsMaximum {
			return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s(n): n là số nguyên từ 1 tới %d", word, queueRunsMaximum))
		}
		return int(args[0].N), 0, value.Value{K: value.Nil}
	case args[0].K == value.Map:
		limit, page := queueRunsDefault, 1
		for key, item := range args[0].Map() {
			switch key {
			case "limit":
				if !whole(item) || item.N > queueRunsMaximum {
					return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("limit là số nguyên từ 1 tới %d", queueRunsMaximum))
				}
				limit = int(item.N)
			case "page":
				if !whole(item) {
					return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, "page là số nguyên từ 1")
				}
				page = int(item.N)
			case "where":
				return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s({ where }) không có: điều kiện viết trước hành động, queue.where(fn).%s()", word, word))
			default:
				return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("%s không nhận %s: %s", word, key, usage))
			}
		}
		return limit, (page - 1) * limit, value.Value{K: value.Nil}
	}
	return 0, 0, value.InvalidFailure(value.InvalidArgumentCode, usage)
}

// runs reads the runs, newest first.
func (r *queueQuery) runs(args []value.Value) value.Value {
	limit, offset, failure := queuePage("runs", args)
	if failure.Failed() {
		return failure
	}
	runs, err := r.tenant.queueStore().Runs(queuestore.Filter{Name: r.job, Status: r.status}, limit, offset)
	if err != nil {
		return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("%s.runs: %v", r.who, err))
	}
	out := make([]value.Value, len(runs))
	for index, run := range runs {
		out[index] = runRecord(run)
	}
	return value.New(out)
}

// list reads the definitions, by name, whether anything was sent or not.
func (r *queueQuery) list(args []value.Value) value.Value {
	if r.job != "" {
		message, _ := compiler.QueueJobRemovedWord("list")
		return value.InvalidFailure(value.InvalidArgumentCode, message)
	}
	if r.status != "" {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`queue.status("%s").list() không có: trạng thái chỉ có ở lần chạy, viết queue.status("%s").runs()`, r.status, r.status))
	}
	limit, offset, failure := queuePage("list", args)
	if failure.Failed() {
		return failure
	}
	if r.tenant.queueWorker() == nil {
		// no .shared/queue/ (removed since): no job is declared, whatever kitwork_definitions kept
		return value.New([]value.Value{})
	}
	definitions, err := r.tenant.queueStore().Definitions(limit, offset)
	if err != nil {
		return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("queue.list: %v", err))
	}
	out := make([]value.Value, len(definitions))
	for index, definition := range definitions {
		timeout := value.Value{K: value.Nil}
		if definition.Timeout != "" {
			timeout = value.New(definition.Timeout)
		}
		out[index] = value.New(map[string]value.Value{
			"name":    value.New(definition.Name),
			"retry":   value.New(definition.Retry),
			"timeout": timeout,
			"source":  value.New(definition.Source),
		})
	}
	return value.New(out)
}

// runRecord is a run as runs() gives it.
func runRecord(run queuestore.Run) value.Value {
	text := func(s string) value.Value {
		if s == "" {
			return value.Value{K: value.Nil}
		}
		return value.New(s)
	}
	record := map[string]value.Value{
		"id":          value.New(run.ID),
		"name":        value.New(run.Name),
		"status":      value.New(run.Status),
		"attempt":     value.New(run.Attempt),
		"maxAttempts": value.New(run.MaxAttempts),
		"data":        decodeJobData(run.Data),
		"key":         text(run.Key),
		"log":         value.New(run.Log),
		"error":       value.Value{K: value.Nil},
		"createdAt":   value.New(run.CreatedAt),
		"startedAt":   text(run.StartedAt),
		"finishedAt":  text(run.FinishedAt),
		"nextRunAt":   value.Value{K: value.Nil},
		"replayOf":    text(run.ReplayOf),
	}
	if run.ErrorCode != "" || run.ErrorMessage != "" {
		record["error"] = value.New(map[string]value.Value{"code": value.New(run.ErrorCode), "message": value.New(run.ErrorMessage)})
	}
	if run.Status == queuestore.Pending {
		record["nextRunAt"] = value.New(run.AvailableAt)
	}
	return value.New(record)
}

// decodeJobData is a run's stored data as a value; data that does not decode is null.
func decodeJobData(data string) value.Value {
	if data == "" {
		return value.Value{K: value.Nil}
	}
	var decoded any
	if err := json.Unmarshal([]byte(data), &decoded); err != nil {
		return value.Value{K: value.Nil}
	}
	return value.New(decoded)
}

// ── cancel(id) and replay(id) (mục 9) ───────────────────────────────────────────────────────────────

// runIDArgument reads the id cancel and replay take: the bare id of a run, never a job's name.
func runIDArgument(word string, args []value.Value) (string, value.Value) {
	if len(args) != 1 || args[0].K != value.String || !strings.HasPrefix(args[0].Text(), "r_") {
		return "", value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`%s nhận id của một lần chạy ("r_…", từ biên nhận của send hay từ runs()), không phải tên job`, word))
	}
	return args[0].Text(), value.Value{K: value.Nil}
}

func queueCancel(tenant *Tenant, who, job string, args []value.Value) value.Value {
	word := who + ".cancel"
	id, failure := runIDArgument(word, args)
	if failure.Failed() {
		return failure
	}
	run, err := tenant.queueStore().Cancel(id, job, time.Now())
	var state *queuestore.StateError
	switch {
	case errors.Is(err, queuestore.ErrNotFound):
		return value.InvalidFailure(value.NotFoundCode, fmt.Sprintf("%s(%q): %s không có lần chạy này", word, id, owner(who)))
	case errors.As(err, &state):
		return value.InvalidFailure(value.ConflictCode, fmt.Sprintf("%s(%q): lần chạy đã %s, không còn gì để huỷ", word, id, state.Run.Status))
	case err != nil:
		return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("%s(%q): %v", word, id, err))
	}
	if run.Status == queuestore.Cancelling {
		tenant.stopQueueAttempt(run.ID)
	}
	return value.New(map[string]value.Value{
		"id": value.New(run.ID), "name": value.New(run.Name), "status": value.New(run.Status),
	})
}

func queueReplay(tenant *Tenant, who, job string, args []value.Value) value.Value {
	word := who + ".replay"
	id, failure := runIDArgument(word, args)
	if failure.Failed() {
		if len(args) > 0 && args[0].K == value.String && !strings.HasPrefix(args[0].Text(), "r_") {
			return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf(`%s(%q): replay nhận id của một lần chạy và tạo một lần chạy mới; chạy lại mọi lần thất bại của một job là đọc %s.status("failed").runs() rồi replay(id) từng lần`, word, args[0].Text(), args[0].Text()))
		}
		return failure
	}
	// a job is replayed only when the app declares it now: kitwork_definitions keeps the jobs of a folder
	// removed since the last boot, which no worker reads
	declared := func(name string) bool {
		worker := tenant.queueWorker()
		return worker != nil && worker.declares(name)
	}
	run, definition, err := tenant.queueStore().Replay(id, job, time.Now(), declared)
	var state *queuestore.StateError
	switch {
	case errors.Is(err, queuestore.ErrNotFound):
		return value.InvalidFailure(value.NotFoundCode, fmt.Sprintf("%s(%q): %s không có lần chạy này (chưa từng gửi, hay đã dọn theo keep)", word, id, owner(who)))
	case errors.Is(err, queuestore.ErrNoDefinition):
		return value.InvalidFailure(value.NotFoundCode, fmt.Sprintf("%s(%q): job %s không còn trong .shared/queue/, không chạy lại được", word, id, definition.Name))
	case errors.As(err, &state):
		return value.InvalidFailure(value.ConflictCode, fmt.Sprintf("%s(%q): lần chạy đang %s; chỉ chạy lại lần chạy đã xong (failed, cancelled, completed)", word, id, state.Run.Status))
	case err != nil:
		return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("%s(%q): %v", word, id, err))
	}
	tenant.wakeQueueWorker()
	return value.New(map[string]value.Value{
		"id": value.New(run.ID), "name": value.New(run.Name), "status": value.New(run.Status),
		"replayOf": value.New(run.ReplayOf),
	})
}

// owner is who has the runs a cancel or a replay looks in: the app, or the job.
func owner(who string) string {
	if who == "queue" {
		return "app"
	}
	return who
}
