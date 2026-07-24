package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/capabilities"
	"github.com/kitwork/engine/value"
)

// CronAdapter is the JS capability adapter for scheduling tasks.
type CronAdapter struct {
	scope capabilities.Scope
}

func NewCronAdapter(scope capabilities.Scope) *CronAdapter {
	return &CronAdapter{scope: scope}
}

func (c *CronAdapter) Every(args ...value.Value) *CronBuilder {
	return (&CronBuilder{adapter: c}).Every(args...)
}
func (c *CronAdapter) Daily(args ...value.Value) *CronBuilder {
	return (&CronBuilder{adapter: c}).Daily(args...)
}
func (c *CronAdapter) Hourly(args ...value.Value) *CronBuilder {
	return (&CronBuilder{adapter: c}).Hourly(args...)
}
func (c *CronAdapter) Weekly(args ...value.Value) *CronBuilder {
	return (&CronBuilder{adapter: c}).Weekly(args...)
}
func (c *CronAdapter) Monthly(args ...value.Value) *CronBuilder {
	return (&CronBuilder{adapter: c}).Monthly(args...)
}
func (c *CronAdapter) Cron(args ...value.Value) *CronBuilder {
	return (&CronBuilder{adapter: c}).Cron(args...)
}

func (c *CronAdapter) List(args ...value.Value) value.Value {
	return value.New([]any{})
}

type CronBuilder struct {
	adapter    *CronAdapter
	expression string

	retentionDays int
	maxAttempts   int
	timezone      string
	overlap       string
}

func (cb *CronBuilder) Every(args ...value.Value) *CronBuilder {
	if len(args) == 0 {
		return cb
	}
	v := args[0]
	if v.K == value.Number && v.N > 0 {
		cb.expression = fmt.Sprintf("*/%d * * * *", int(v.N))
		return cb
	}
	if v.K == value.String {
		s := strings.TrimSpace(v.Text())
		if strings.HasPrefix(s, "*/") {
			cb.expression = s
			return cb
		}
		if num, err := strconv.Atoi(s); err == nil && num > 0 {
			cb.expression = fmt.Sprintf("*/%d * * * *", num)
			return cb
		}
		if d, err := time.ParseDuration(s); err == nil && d >= time.Minute {
			cb.expression = fmt.Sprintf("*/%d * * * *", int(d.Minutes()))
			return cb
		}
	}
	return cb
}

func (cb *CronBuilder) Daily(args ...value.Value) *CronBuilder {
	timeStr := "00:00"
	if len(args) > 0 && args[0].K == value.String && args[0].Text() != "" {
		timeStr = args[0].Text()
	}
	hour, min := 0, 0
	parts := strings.Split(timeStr, ":")
	if len(parts) >= 2 {
		hour, _ = strconv.Atoi(parts[0])
		min, _ = strconv.Atoi(parts[1])
	}
	cb.expression = fmt.Sprintf("%d %d * * *", min, hour)
	return cb
}

func (cb *CronBuilder) Hourly(args ...value.Value) *CronBuilder {
	min := 0
	if len(args) > 0 && args[0].K == value.Number {
		min = int(args[0].N)
	}
	cb.expression = fmt.Sprintf("%d * * * *", min)
	return cb
}

func (cb *CronBuilder) Weekly(args ...value.Value) *CronBuilder {
	cb.expression = "0 0 * * 0"
	return cb
}

func (cb *CronBuilder) Monthly(args ...value.Value) *CronBuilder {
	cb.expression = "0 0 1 * *"
	return cb
}

func (cb *CronBuilder) Cron(args ...value.Value) *CronBuilder {
	if len(args) > 0 && args[0].K == value.String {
		cb.expression = args[0].Text()
	}
	return cb
}

func (cb *CronBuilder) Handle(args ...value.Value) *CronBuilder {
	return cb
}

func Register(registry *capabilities.Registry) {
	registry.Register("cron", func(scope capabilities.Scope) value.Value {
		return value.New(NewCronAdapter(scope))
	})
}

func init() {
	Register(capabilities.DefaultRegistry)
}
