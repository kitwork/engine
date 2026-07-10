package work

import (
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

func limitMap(kv map[string]any) value.Value {
	m := map[string]value.Value{}
	for k, v := range kv {
		m[k] = value.New(v)
	}
	return value.New(m)
}

// The .ratelimit() window can be a UNIT key — { ip: 30, second: 1 } reads "30 per second" — the
// same shape the flat-era guards exported ({ rate: 30, second: 1 }), so old guard objects keep
// working. period/per stays the explicit form and WINS when both are present.
func TestRatelimitRulesUnitKeys(t *testing.T) {
	cases := []struct {
		name    string
		in      value.Value
		wantDim string
		wantN   int
		wantPer time.Duration
	}{
		{"second unit", limitMap(map[string]any{"ip": 30, "second": 1}), "ip", 30, time.Second},
		{"minute unit", limitMap(map[string]any{"ip": 600, "minute": 1}), "ip", 600, time.Minute},
		{"multi-minute window", limitMap(map[string]any{"ip": 100, "minute": 5}), "ip", 100, 5 * time.Minute},
		{"hour plural", limitMap(map[string]any{"browser": 1000, "hours": 2}), "browser", 1000, 2 * time.Hour},
		{"day", limitMap(map[string]any{"global": 100000, "day": 1}), "global", 100000, 24 * time.Hour},
		{"legacy guards shape", limitMap(map[string]any{"rate": 30, "second": 1}), "ip", 30, time.Second},
		{"explicit period wins over unit", limitMap(map[string]any{"ip": 9, "period": "2m", "second": 1}), "ip", 9, 2 * time.Minute},
		{"no window at all → default 1s", limitMap(map[string]any{"ip": 7}), "ip", 7, time.Second},
	}
	for _, c := range cases {
		rules := ratelimitRules(c.in)
		if len(rules) != 1 {
			t.Errorf("%s: want 1 rule, got %d", c.name, len(rules))
			continue
		}
		r := rules[0]
		if r.Dim != c.wantDim || r.Rate != c.wantN || r.Per != c.wantPer {
			t.Errorf("%s: got {%s %d %v}, want {%s %d %v}", c.name, r.Dim, r.Rate, r.Per, c.wantDim, c.wantN, c.wantPer)
		}
	}

	// Stacking across calls still yields separate rules.
	rules := ratelimitRules(
		limitMap(map[string]any{"ip": 30, "second": 1}),
		limitMap(map[string]any{"ip": 600, "minute": 1}),
	)
	if len(rules) != 2 || rules[0].Per == rules[1].Per {
		t.Errorf("stacked unit windows: got %+v", rules)
	}
}
