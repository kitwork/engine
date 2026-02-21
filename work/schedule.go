package work

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// ScheduleRule đại diện cho một rule lập lịch (Cron)
type ScheduleRule struct {
	Cron    string
	Handler *value.Script
}

type ScheduleCore struct {
	Schedules      []*ScheduleRule
	PendingHandler *value.Script
}

// Schedule is the universal entry point for recurring tasks.
func (w *Work) Schedule(args ...value.Value) *Work {
	if len(args) == 0 {
		return w
	}

	var handler *value.Script
	var definitions []string

	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			handler = sFn
			w.CoreSchedule.PendingHandler = sFn // Store for fluent chaining
		} else {
			if arg.IsNumeric() {
				definitions = append(definitions, fmt.Sprintf("%v", arg.Interface()))
			} else {
				definitions = append(definitions, arg.Text())
			}
		}
	}

	// Use pending handler if none provided in args
	if handler == nil {
		handler = w.CoreSchedule.PendingHandler
	}
	// Fallback to MainHandler if still nil
	if handler == nil {
		handler = w.MainHandler
	}

	for _, def := range definitions {
		cronExpr := smartParse(def)
		if cronExpr != "" && handler != nil {
			w.registerSchedule(cronExpr, handler)
		}
	}

	return w
}

// Cron is an alias for Schedule, supporting the same universal parsing.
func (w *Work) Cron(args ...value.Value) *Work {
	return w.Schedule(args...)
}

// Every registers tasks to run every N duration.
func (w *Work) Every(args ...value.Value) *Work {
	var handler *value.Script
	var durations []string
	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			handler = sFn
			w.CoreSchedule.PendingHandler = sFn
		} else {
			durations = append(durations, arg.Text())
		}
	}

	if handler == nil {
		handler = w.CoreSchedule.PendingHandler
	}
	if handler == nil {
		handler = w.MainHandler
	}

	for _, d := range durations {
		w.registerSchedule("@every "+d, handler)
	}
	return w
}

// Daily registers tasks for specific times. .daily("13:00", "01:00")
func (w *Work) Daily(args ...value.Value) *Work {
	return w.Schedule(args...)
}

// Hourly registers tasks for specific minutes. .hourly(0, 15, "30", "45")
func (w *Work) Hourly(args ...value.Value) *Work {
	var handler *value.Script
	var mins []string
	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			handler = sFn
			w.CoreSchedule.PendingHandler = sFn
		} else {
			if arg.IsNumeric() {
				mins = append(mins, fmt.Sprintf("%d", int(arg.N)))
			} else {
				mins = append(mins, arg.Text())
			}
		}
	}

	if handler == nil {
		handler = w.CoreSchedule.PendingHandler
	}
	if handler == nil {
		handler = w.MainHandler
	}

	if len(mins) == 0 {
		w.registerSchedule("@hourly", handler)
	} else {
		for _, m := range mins {
			// Clean numeric or semantic minute
			cleanM := strings.TrimRight(strings.ToLower(m), "m")
			w.registerSchedule(fmt.Sprintf("0 %s * * * *", cleanM), handler)
		}
	}
	return w
}

// Weekly registers tasks for specific days. .weekly("MON 09:00", "FRI 17:00")
func (w *Work) Weekly(args ...value.Value) *Work {
	var handler *value.Script
	var hasDefinition bool
	var definitions []value.Value

	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			handler = sFn
			w.CoreSchedule.PendingHandler = sFn
		} else {
			hasDefinition = true
			definitions = append(definitions, arg)
		}
	}

	if handler == nil {
		handler = w.CoreSchedule.PendingHandler
	}
	if handler == nil {
		handler = w.MainHandler
	}

	if !hasDefinition {
		w.registerSchedule("@weekly", handler)
		return w
	}
	return w.Schedule(definitions...)
}

// Monthly registers tasks for specific days of month. .monthly("1st 12:00")
func (w *Work) Monthly(args ...value.Value) *Work {
	var handler *value.Script
	var hasDefinition bool
	var patterns []string

	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			handler = sFn
			w.CoreSchedule.PendingHandler = sFn
		} else {
			hasDefinition = true
			patterns = append(patterns, arg.Text())
		}
	}

	if handler == nil {
		handler = w.CoreSchedule.PendingHandler
	}
	if handler == nil {
		handler = w.MainHandler
	}

	if !hasDefinition {
		w.registerSchedule("@monthly", handler)
		return w
	}

	for _, p := range patterns {
		w.registerSchedule(monthlyParse(p), handler)
	}
	return w
}

func (w *Work) registerSchedule(cron string, handler *value.Script) {
	if cron == "" || handler == nil {
		return
	}
	for i, s := range w.CoreSchedule.Schedules {
		if s.Cron == cron {
			w.CoreSchedule.Schedules[i].Handler = handler
			return
		}
	}
	w.CoreSchedule.Schedules = append(w.CoreSchedule.Schedules, &ScheduleRule{Cron: cron, Handler: handler})
}

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
	// Time: 13:00 -> 0 0 13 * * *
	if matched, _ := regexp.MatchString(`^\d{1,2}:\d{2}(:\d{2})?$`, input); matched {
		return timeToCron(input)
	}
	// Day Time: MONDAY 13:00
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

// monthlyParse handles "1st 12:00", "15 08:00"
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
		parts[3] = day // Set Day of Month
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
