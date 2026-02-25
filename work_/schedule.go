package work

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// Cron đại diện cho một tác vụ lập lịch
type Cron struct {
	Work
	Schedules []string
}

func (c *Cron) Handle(fn value.Value) *Cron {
	if sFn, ok := fn.V.(*value.Script); ok {
		c.handle = sFn
	}
	return c
}

func (c *Cron) registerSchedule(cronExpr string) {
	if cronExpr == "" {
		return
	}
	for _, s := range c.Schedules {
		if s == cronExpr {
			return
		}
	}
	c.Schedules = append(c.Schedules, cronExpr)
}

// Schedule is the universal entry point for recurring tasks.
func (c *Cron) Schedule(args ...value.Value) *Cron {
	if len(args) == 0 {
		return c
	}

	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			c.handle = sFn
		} else {
			def := ""
			if arg.IsNumeric() {
				def = fmt.Sprintf("%v", arg.Interface())
			} else {
				def = arg.Text()
			}
			cronExpr := smartParse(def)
			if cronExpr != "" {
				c.registerSchedule(cronExpr)
			}
		}
	}

	return c
}

// Every registers tasks to run every N duration.
func (c *Cron) Every(args ...value.Value) *Cron {
	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			c.handle = sFn
		} else {
			c.registerSchedule("@every " + arg.Text())
		}
	}
	return c
}

// Daily registers tasks for specific times. .daily("13:00", "01:00")
func (c *Cron) Daily(args ...value.Value) *Cron {
	return c.Schedule(args...)
}

// Hourly registers tasks for specific minutes. .hourly(0, 15, "30", "45")
func (c *Cron) Hourly(args ...value.Value) *Cron {
	var mins []string
	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			c.handle = sFn
		} else {
			if arg.IsNumeric() {
				mins = append(mins, fmt.Sprintf("%d", int(arg.N)))
			} else {
				mins = append(mins, arg.Text())
			}
		}
	}

	if len(mins) == 0 {
		c.registerSchedule("@hourly")
	} else {
		for _, m := range mins {
			// Clean numeric or semantic minute
			cleanM := strings.TrimRight(strings.ToLower(m), "m")
			c.registerSchedule(fmt.Sprintf("0 %s * * * *", cleanM))
		}
	}
	return c
}

// Weekly registers tasks for specific days. .weekly("MON 09:00", "FRI 17:00")
func (c *Cron) Weekly(args ...value.Value) *Cron {
	var hasDefinition bool

	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			c.handle = sFn
		} else {
			hasDefinition = true
			c.Schedule(arg)
		}
	}

	if !hasDefinition {
		c.registerSchedule("@weekly")
	}
	return c
}

// Monthly registers tasks for specific days of month. .monthly("1st 12:00")
func (c *Cron) Monthly(args ...value.Value) *Cron {
	var hasDefinition bool

	for _, arg := range args {
		if sFn, ok := arg.V.(*value.Script); ok {
			c.handle = sFn
		} else {
			hasDefinition = true
			c.registerSchedule(monthlyParse(arg.Text()))
		}
	}

	if !hasDefinition {
		c.registerSchedule("@monthly")
	}

	return c
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
