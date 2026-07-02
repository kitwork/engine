package work

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// CronJob represents a scheduled task registered by a tenant
type CronJob struct {
	Expression string
	Callback   *value.Lambda
}

// Cron is the entry namespace for scheduled tasks in the VM
type Cron struct {
	tenant *Tenant
}

func (w *KitWork) Cron() *Cron {
	return &Cron{tenant: w.tenant}
}

// CronBuilder is a fluent builder for scheduled task setups
type CronBuilder struct {
	cron       *Cron
	expression string
}

func (c *Cron) Every(args ...value.Value) *CronBuilder {
	if len(args) == 0 {
		return &CronBuilder{cron: c, expression: ""}
	}
	expr := smartParse(args[0].Text())
	cb := &CronBuilder{cron: c, expression: expr}
	if len(args) > 1 && args[1].IsCallable() {
		cb.Handle(args[1])
	}
	return cb
}

func (c *Cron) Schedule(args ...value.Value) *CronBuilder {
	if len(args) == 0 {
		return &CronBuilder{cron: c, expression: ""}
	}
	expr := smartParse(args[0].Text())
	cb := &CronBuilder{cron: c, expression: expr}
	if len(args) > 1 && args[1].IsCallable() {
		cb.Handle(args[1])
	}
	return cb
}

func (c *Cron) Daily(args ...value.Value) *CronBuilder {
	if len(args) == 0 {
		return &CronBuilder{cron: c, expression: "@daily"}
	}
	expr := timeToCron(args[0].Text())
	cb := &CronBuilder{cron: c, expression: expr}
	if len(args) > 1 && args[1].IsCallable() {
		cb.Handle(args[1])
	}
	return cb
}

func (c *Cron) Hourly(args ...value.Value) *CronBuilder {
	min := "0"
	if len(args) > 0 && !args[0].IsCallable() {
		min = args[0].Text()
	}
	expr := fmt.Sprintf("0 %s * * * *", strings.TrimRight(strings.ToLower(min), "m"))
	cb := &CronBuilder{cron: c, expression: expr}
	for _, arg := range args {
		if arg.IsCallable() {
			cb.Handle(arg)
			break
		}
	}
	return cb
}

func (c *Cron) Weekly(args ...value.Value) *CronBuilder {
	expr := "@weekly"
	if len(args) > 0 && !args[0].IsCallable() {
		expr = smartParse(args[0].Text())
	}
	cb := &CronBuilder{cron: c, expression: expr}
	for _, arg := range args {
		if arg.IsCallable() {
			cb.Handle(arg)
			break
		}
	}
	return cb
}

func (c *Cron) Monthly(args ...value.Value) *CronBuilder {
	expr := "@monthly"
	if len(args) > 0 && !args[0].IsCallable() {
		expr = monthlyParse(args[0].Text())
	}
	cb := &CronBuilder{cron: c, expression: expr}
	for _, arg := range args {
		if arg.IsCallable() {
			cb.Handle(arg)
			break
		}
	}
	return cb
}

func (cb *CronBuilder) Handle(callback value.Value) *CronBuilder {
	if callback.IsCallable() && cb.expression != "" {
		lambda, _ := callback.V.(*value.Lambda)
		cb.cron.tenant.RegisterCron(cb.expression, lambda)
	}
	return cb
}

// RegisterCron appends a cron job configuration to the tenant's registry.
func (t *Tenant) RegisterCron(expression string, callback *value.Lambda) {
	t.cronMu.Lock()
	defer t.cronMu.Unlock()
	t.crons = append(t.crons, &CronJob{
		Expression: expression,
		Callback:   callback,
	})
}

// StartCronJobs spawns goroutines to run all registered background cron tasks.
func (t *Tenant) StartCronJobs() {
	t.cronMu.Lock()
	defer t.cronMu.Unlock()

	// Stop any existing jobs first to be safe
	t.stopCronJobsNoLock()

	for _, job := range t.crons {
		cancelChan := make(chan struct{})
		t.cronCancels = append(t.cronCancels, cancelChan)

		// Run job in background goroutine
		go t.runCronJob(job, cancelChan)
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

func (t *Tenant) runCronJob(job *CronJob, cancel chan struct{}) {
	var duration time.Duration
	var err error

	expr := job.Expression
	isInterval := false
	if strings.HasPrefix(expr, "@every ") {
		isInterval = true
		durationStr := strings.TrimPrefix(expr, "@every ")
		duration, err = ParseDuration(durationStr)
		if err != nil {
			fmt.Printf("[Cron Error] Invalid duration: %s\n", durationStr)
			return
		}
	} else if strings.HasPrefix(expr, "@hourly") {
		isInterval = true
		duration = time.Hour
	} else if strings.HasPrefix(expr, "@daily") {
		isInterval = true
		duration = 24 * time.Hour
	} else if strings.HasPrefix(expr, "@weekly") {
		isInterval = true
		duration = 7 * 24 * time.Hour
	} else if strings.HasPrefix(expr, "@monthly") {
		isInterval = true
		duration = 30 * 24 * time.Hour
	}

	if isInterval {
		ticker := time.NewTicker(duration)
		defer ticker.Stop()

		for {
			select {
			case <-cancel:
				return
			case <-ticker.C:
				t.executeCronCallback(job.Callback)
			}
		}
	} else {
		// standard cron expression matcher (ticks every second, runs once per matching minute)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		lastRunMinute := -1

		for {
			select {
			case <-cancel:
				return
			case <-ticker.C:
				now := time.Now()
				if matchCronExpression(expr, now) {
					currentMinute := now.Minute()
					if lastRunMinute != currentMinute {
						lastRunMinute = currentMinute
						t.executeCronCallback(job.Callback)
					}
				}
			}
		}
	}
}

func (t *Tenant) executeCronCallback(lambda *value.Lambda) {
	vmInterface := vmPool.Get()
	if vmInterface == nil {
		return
	}
	vm, ok := vmInterface.(*runtime.VM)
	if !ok {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("[Cron Job] Panic: %v\n", r)
			}
			vmPool.Put(vm)
		}()

		vm.FastReset(t.bytecode.Instructions, t.bytecode.Constants, t.vm.Globals, t.bytecode.SourceMap)
		vm.MaxEnergy = t.MaxEnergy

		for key, val := range t.vm.Vars {
			vm.Vars[key] = val
		}

		vm.ExecuteLambda(lambda, nil)
	}()
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
