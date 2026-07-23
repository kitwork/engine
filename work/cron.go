package work

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// CronJob is a registered scheduled task. The callback's Address offsets point into Bytecode — each
// _cron/*.kitwork.js compiles to its OWN bytecode (like a folder router), so runInJobVM FastResets
// THAT bytecode (not the tenant's main bytecode) before invoking the lambda.
//
// EVERY file-cron is durable: its definition is stored in the `crons` table and it is dispatched through
// the database (idempotent slots, history, cross-node coordination). There is no in-process-only tier
// and no .persist() flag — a cron in a file is, by nature, stored and survives a restart.
type CronJob struct {
	Name       string
	Expression string
	Callback   *value.Lambda
	Bytecode   *compiler.Bytecode

	RetentionDays int
	MaxAttempts   int    // default 1 — scheduler does NOT auto-retry a thrown handler unless .retries(n)
	Timezone      string // default "UTC"
	OverlapPolicy string // skip | queue | allow — default skip
	ContentHash   string // sha256 of the source file; lets sync skip no-op writes
	OnSuccess     *value.Lambda
	OnError       *value.Lambda
}

// Cron is the tenant's scheduler namespace: `import { cron } from "kitwork"`. It mirrors the router
// convention — a noun subsystem you call methods on (cron.schedule(...), cron.daily(...)), reached
// via kitwork().cron (the 0-arg method is auto-called as a getter, like kitwork().router).
type Cron struct {
	tenant *Tenant
}

func (w *KitWork) Cron() *Cron {
	return &Cron{tenant: w.tenant}
}

// Cadence entry points start a builder. A cron's identity is its FILE — one file, one cron, named by
// the filename (filesystem-is-runtime, exactly like a route's path is its identity). So there is no
// cron.schedule("name"): `cron.daily("08:00").handle(fn)` inside _cron/daily-report.kitwork.js IS the
// "daily-report" cron (the stem is applied in runCronFile). Group related jobs as separate files that
// share logic via _core, not many crons in one file.
func (c *Cron) Every(args ...value.Value) *CronBuilder {
	return (&CronBuilder{tenant: c.tenant}).Every(args...)
}
func (c *Cron) Daily(args ...value.Value) *CronBuilder {
	return (&CronBuilder{tenant: c.tenant}).Daily(args...)
}
func (c *Cron) Hourly(args ...value.Value) *CronBuilder {
	return (&CronBuilder{tenant: c.tenant}).Hourly(args...)
}
func (c *Cron) Weekly(args ...value.Value) *CronBuilder {
	return (&CronBuilder{tenant: c.tenant}).Weekly(args...)
}
func (c *Cron) Monthly(args ...value.Value) *CronBuilder {
	return (&CronBuilder{tenant: c.tenant}).Monthly(args...)
}
func (c *Cron) Cron(args ...value.Value) *CronBuilder {
	return (&CronBuilder{tenant: c.tenant}).Cron(args...)
}

// CronBuilder is the fluent schedule builder. Scheduling methods set the expression; .handle()
// registers. Config modifiers (.persist/.retries/.timezone/.overlap/.success/.error) may be chained
// EITHER side of .handle(): before, they stage onto the builder and .handle() copies them into the new
// job; after, they mutate the already-registered job (cb.job) in place — so an option chained after
// .handle() (as .retention("30d") or .success(fn) often is) still takes effect.
type CronBuilder struct {
	tenant     *Tenant
	expression string
	job        *CronJob // set once .handle() registers; post-handle modifiers mutate this

	// staged config, copied into the job at .handle() time (for the pre-handle order)
	retentionDays int
	maxAttempts   int
	timezone      string
	overlap       string
	onSuccess     *value.Lambda
	onError       *value.Lambda
}

// maybeHandle registers the callback when it is passed inline, e.g. .daily("08:00", fn).
func (cb *CronBuilder) maybeHandle(args []value.Value) *CronBuilder {
	for _, a := range args {
		if a.IsCallable() {
			cb.Handle(a)
			break
		}
	}
	return cb
}

func (cb *CronBuilder) Every(args ...value.Value) *CronBuilder {
	if len(args) > 0 && !args[0].IsCallable() {
		cb.expression = smartParse(args[0].Text())
	}
	return cb.maybeHandle(args)
}

// Cron sets a raw cron expression verbatim (escape hatch): .cron("*/5 * * * *").
func (cb *CronBuilder) Cron(args ...value.Value) *CronBuilder {
	if len(args) > 0 && !args[0].IsCallable() {
		cb.expression = smartParse(args[0].Text())
	}
	return cb.maybeHandle(args)
}

func (cb *CronBuilder) Daily(args ...value.Value) *CronBuilder {
	cb.expression = "@daily"
	if len(args) > 0 && !args[0].IsCallable() {
		cb.expression = timeToCron(args[0].Text())
	}
	return cb.maybeHandle(args)
}

func (cb *CronBuilder) Hourly(args ...value.Value) *CronBuilder {
	min := "0"
	if len(args) > 0 && !args[0].IsCallable() {
		min = args[0].Text()
	}
	cb.expression = fmt.Sprintf("0 %s * * * *", strings.TrimRight(strings.ToLower(min), "m"))
	return cb.maybeHandle(args)
}

func (cb *CronBuilder) Weekly(args ...value.Value) *CronBuilder {
	cb.expression = "@weekly"
	if len(args) > 0 && !args[0].IsCallable() {
		cb.expression = smartParse(args[0].Text())
	}
	return cb.maybeHandle(args)
}

func (cb *CronBuilder) Monthly(args ...value.Value) *CronBuilder {
	cb.expression = "@monthly"
	if len(args) > 0 && !args[0].IsCallable() {
		cb.expression = monthlyParse(args[0].Text())
	}
	return cb.maybeHandle(args)
}

// Retention sets how long this cron's run history is kept in the `crons` store, e.g. .retention("90d")
// (default 30 days). Durability itself is NOT optional — every file-cron is stored + survives restart;
// this only bounds how much past history is retained. (.persist() is a fetch-only modifier and does not
// exist on cron.)
func (cb *CronBuilder) Retention(args ...value.Value) *CronBuilder {
	if len(args) > 0 && !args[0].IsCallable() {
		if d, err := ParseDuration(args[0].Text()); err == nil {
			cb.retentionDays = int(d.Hours() / 24)
		}
	}
	if cb.retentionDays < 1 {
		cb.retentionDays = 1
	}
	if cb.job != nil {
		cb.job.RetentionDays = cb.retentionDays
	}
	return cb
}

// Retries opts a job into scheduler-level retry (default max_attempts is 1 — see scheduler.md; a thrown
// handler re-runs ALL side effects, so this is only for handlers the author made idempotent).
func (cb *CronBuilder) Retries(args ...value.Value) *CronBuilder {
	cb.maxAttempts = 1
	if len(args) > 0 && !args[0].IsCallable() {
		if n, err := strconv.Atoi(strings.TrimSpace(args[0].Text())); err == nil && n >= 1 {
			cb.maxAttempts = n
		}
	}
	if cb.job != nil {
		cb.job.MaxAttempts = cb.maxAttempts
	}
	return cb
}

func (cb *CronBuilder) Timezone(args ...value.Value) *CronBuilder {
	if len(args) > 0 && !args[0].IsCallable() {
		cb.timezone = args[0].Text()
	}
	if cb.job != nil {
		cb.job.Timezone = cb.timezone
	}
	return cb
}

func (cb *CronBuilder) Overlap(args ...value.Value) *CronBuilder {
	if len(args) > 0 && !args[0].IsCallable() {
		cb.overlap = strings.ToLower(strings.TrimSpace(args[0].Text()))
	}
	if cb.job != nil {
		cb.job.OverlapPolicy = cb.overlap
	}
	return cb
}

func (cb *CronBuilder) Success(args ...value.Value) *CronBuilder {
	if len(args) > 0 {
		cb.onSuccess = lambdaOf(args[0])
	}
	if cb.job != nil {
		cb.job.OnSuccess = cb.onSuccess
	}
	return cb
}

func (cb *CronBuilder) Error(args ...value.Value) *CronBuilder {
	if len(args) > 0 {
		cb.onError = lambdaOf(args[0])
	}
	if cb.job != nil {
		cb.job.OnError = cb.onError
	}
	return cb
}

// Misfire — accepted + chainable; the misfire policy is a Phase-2-plus refinement (catch-up on missed
// slots) not yet wired, so this stays a no-op passthrough for now.
func (cb *CronBuilder) Misfire(args ...value.Value) *CronBuilder { return cb }

func (cb *CronBuilder) Handle(callback value.Value) *CronBuilder {
	if callback.IsCallable() && cb.expression != "" {
		lambda, _ := callback.V.(*value.Lambda)
		cb.job = cb.tenant.RegisterCron(cb.expression, lambda)
		// Copy any config staged BEFORE .handle() into the freshly-registered job.
		cb.job.RetentionDays = cb.retentionDays
		cb.job.MaxAttempts = cb.maxAttempts
		cb.job.Timezone = cb.timezone
		cb.job.OverlapPolicy = cb.overlap
		cb.job.OnSuccess = cb.onSuccess
		cb.job.OnError = cb.onError
	}
	return cb
}

// RegisterCron appends a cron job to the tenant's registry and returns it so the caller can finish
// filling it in. The NAME is not set here — it comes from the filename, applied by runCronFile once the
// whole file has been evaluated (one file = one cron).
func (t *Tenant) RegisterCron(expression string, callback *value.Lambda) *CronJob {
	t.cronMu.Lock()
	defer t.cronMu.Unlock()
	job := &CronJob{
		Expression: expression,
		Callback:   callback,
	}
	t.crons = append(t.crons, job)
	return job
}

// LoadCronFiles eagerly evaluates every _cron/*.kitwork.js at boot so scheduled jobs REGISTER before
// any request arrives — folder routers compile lazily on first hit, which is too late for a scheduler
// that must tick on its own. Each file compiles to its OWN bytecode; the jobs it registers keep a
// pointer to that bytecode so their callback addresses resolve when they fire.
//
// `_cron` lives at the IDENTITY (app) level — apps/<identity>/_cron — not per domain: a cron is app
// infrastructure shared by all of the app's domains, keyed by identity (see appID). Every domain of the
// app loads the same set and they coordinate through the one store.
func (t *Tenant) LoadCronFiles() {
	dir := t.resolveApp("_cron")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no _cron/ folder — nothing to schedule
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".kitwork.js") {
			continue
		}
		file := filepath.Join(dir, e.Name())
		bc, err := compiler.CompileFile(file)
		if err != nil {
			fmt.Printf("[Cron] compile %s: %v\n", e.Name(), err)
			continue
		}
		// content_hash of the source lets sync skip no-op writes to scheduler.db (see syncPersisted).
		hash := ""
		if raw, rerr := os.ReadFile(file); rerr == nil {
			sum := sha256.Sum256(raw)
			hash = hex.EncodeToString(sum[:])
		}
		// The filename (minus .kitwork.js) is the DEFAULT job identity — filesystem-is-runtime, same as
		// routes take their identity from their path. Jobs that called cron.schedule("name") keep their
		// explicit name; the rest inherit this stem.
		stem := strings.TrimSuffix(e.Name(), ".kitwork.js")
		t.runCronFile(bc, stem, hash)
	}

	t.StartCronJobs()
}

// runCronFile executes a compiled _cron file in an isolated VM whose kitwork() is the plain tenant
// binding (so `import { cron } from "kitwork"` registers here), then finalizes the job the file
// registered: attaches bc (so the callback's bytecode FastResets at fire time) and NAMES it after the
// file (the stem). ONE FILE = ONE CRON, named by the filename — if a file registers more than one, only
// the first is kept (the rest would collide on the same filename identity) and a warning is logged.
// Globals are COPIED so a cron file's top-level declarations never leak into the shared tenant VM.
func (t *Tenant) runCronFile(bc *compiler.Bytecode, stem, contentHash string) {
	t.cronMu.Lock()
	before := len(t.crons)
	t.cronMu.Unlock()

	globals := make(map[string]value.Value, len(t.vm.Globals))
	for k, v := range t.vm.Globals {
		globals[k] = v
	}

	vm := runtime.New(bc.Instructions, bc.Constants)
	vm.Builtins = t.vm.Builtins
	vm.Globals = globals
	vm.SourceMap = bc.SourceMap
	vm.MaxEnergy = t.MaxEnergy
	vm.Run()

	t.cronMu.Lock()
	registered := t.crons[before:]
	if len(registered) > 1 {
		fmt.Printf("[Cron] %s.kitwork.js registered %d crons; one file = one cron — keeping only %q\n",
			stem, len(registered), stem)
		t.crons = t.crons[:before+1] // drop the extras; they'd collide on the filename identity
		registered = t.crons[before:]
	}
	for i := range registered {
		registered[i].Bytecode = bc
		registered[i].ContentHash = contentHash
		registered[i].Name = stem // the file IS the cron's identity
	}
	t.cronMu.Unlock()
}

// StartCronJobs launches this tenant's scheduler. EVERY registered file-cron is durable — its definition
// is synced into the `crons` table and it is dispatched through the database (idempotent slots, history,
// cross-node coordination). One dispatcher goroutine serves them all; see cron_persist.go.
func (t *Tenant) StartCronJobs() {
	t.cronMu.Lock()
	defer t.cronMu.Unlock()

	t.stopCronJobsNoLock() // stop any existing jobs first to be safe

	if len(t.crons) == 0 {
		return
	}
	if err := t.startPersistedScheduler(); err != nil {
		fmt.Printf("[Cron] scheduler disabled: %v\n", err)
	}
}

// StopCronJobs halts all background cron tasks.
func (t *Tenant) StopCronJobs() {
	t.cronMu.Lock()
	defer t.cronMu.Unlock()
	t.stopCronJobsNoLock()
}

func (t *Tenant) stopCronJobsNoLock() {
	for _, ch := range t.cronCancels {
		close(ch)
	}
	t.cronCancels = nil
}

// Helpers for cron expression parsing

func smartParse(input string) string {
	input = strings.TrimSpace(strings.ToUpper(input))
	if input == "" {
		return ""
	}
	if isNumeric(input) {
		return "@every " + input + "ms"
	}
	if matched, _ := regexp.MatchString(`^\d+(MS|S|M|H)$`, input); matched {
		return "@every " + strings.ToLower(input)
	}
	if matched, _ := regexp.MatchString(`^\d{1,2}:\d{2}(:\d{2})?$`, input); matched {
		return timeToCron(input)
	}
	dayMap := map[string]string{
		"SUNDAY": "0", "SUN": "0", "MONDAY": "1", "MON": "1", "TUESDAY": "2", "TUE": "2",
		"WEDNESDAY": "3", "WED": "3", "THURSDAY": "4", "THU": "4", "FRIDAY": "5", "FRI": "5",
		"SATURDAY": "6", "SAT": "6",
	}
	for dayName, dayIdx := range dayMap {
		if strings.HasPrefix(input, dayName) {
			timePart := strings.TrimSpace(strings.TrimPrefix(input, dayName))
			if timePart == "" {
				timePart = "00:00"
			}
			parts := strings.Split(timeToCron(timePart), " ")
			if len(parts) >= 6 {
				parts[5] = dayIdx
				return strings.Join(parts, " ")
			}
		}
	}
	return strings.ToLower(input)
}

func monthlyParse(input string) string {
	input = strings.TrimSpace(strings.ToUpper(input))
	re := regexp.MustCompile(`(\d+)(ST|ND|RD|TH)?`)
	m := re.FindStringSubmatch(input)
	if len(m) < 2 {
		return "@monthly"
	}

	day := m[1]
	timePart := strings.TrimSpace(strings.TrimPrefix(input, m[0]))
	if timePart == "" {
		timePart = "00:00"
	}

	cronTime := timeToCron(timePart)
	parts := strings.Split(cronTime, " ")
	if len(parts) >= 6 {
		parts[3] = day
		return strings.Join(parts, " ")
	}
	return "@monthly"
}

func timeToCron(t string) string {
	t = strings.TrimSpace(t)
	if t == "" || strings.HasPrefix(t, "@") {
		return t
	}
	parts := strings.Split(t, ":")
	h, m, s := 0, 0, 0
	if len(parts) >= 1 {
		h, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		m, _ = strconv.Atoi(parts[1])
	}
	if len(parts) >= 3 {
		s, _ = strconv.Atoi(parts[2])
	}
	return fmt.Sprintf("%d %d %d * * *", s, m, h)
}

func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}

	numStr := ""
	unit := ""
	for i, r := range s {
		if r >= '0' && r <= '9' {
			numStr += string(r)
		} else {
			unit = s[i:]
			break
		}
	}

	val, err := strconv.Atoi(numStr)
	if err != nil || val <= 0 {
		return 0, fmt.Errorf("invalid duration format: %s", s)
	}

	switch unit {
	case "d":
		return time.Duration(val) * 24 * time.Hour, nil
	case "w":
		return time.Duration(val) * 7 * 24 * time.Hour, nil
	case "mo":
		return time.Duration(val) * 30 * 24 * time.Hour, nil
	case "y":
		return time.Duration(val) * 365 * 24 * time.Hour, nil
	}

	return 0, fmt.Errorf("unknown duration unit: %s", unit)
}

func matchCronExpression(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) < 5 {
		return false
	}

	var secField, minField, hourField, domField, monthField, dowField string
	if len(fields) == 5 {
		secField = "0"
		minField = fields[0]
		hourField = fields[1]
		domField = fields[2]
		monthField = fields[3]
		dowField = fields[4]
	} else {
		secField = fields[0]
		minField = fields[1]
		hourField = fields[2]
		domField = fields[3]
		monthField = fields[4]
		dowField = fields[5]
	}

	matchField := func(field string, value int) bool {
		if field == "*" || field == "?" {
			return true
		}
		parts := strings.Split(field, ",")
		for _, part := range parts {
			if part == fmt.Sprintf("%d", value) {
				return true
			}
			if strings.Contains(part, "-") {
				var start, end int
				_, err := fmt.Sscanf(part, "%d-%d", &start, &end)
				if err == nil && value >= start && value <= end {
					return true
				}
			}
			if strings.HasPrefix(part, "*/") {
				var step int
				_, err := fmt.Sscanf(part, "*/%d", &step)
				if err == nil && step > 0 && value%step == 0 {
					return true
				}
			}
		}
		return false
	}

	return matchField(secField, t.Second()) &&
		matchField(minField, t.Minute()) &&
		matchField(hourField, t.Hour()) &&
		matchField(domField, t.Day()) &&
		matchField(monthField, int(t.Month())) &&
		matchField(dowField, int(t.Weekday()))
}
