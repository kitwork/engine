package engine

import (
	"strings"
	"testing"
)

// app.history({ days }) of application.work is how long the history of the apps' databases keeps
// (KITWORK_KITDB.md câu 13.6): a whole number of days, 30 when the manifest says nothing.
//
// CONTROL (03/11/2026): with days not read into the configuration, "app.history({ days: 90 }); keeps 0 days, want
// 90"; with a fraction taken as days, "app.history({ days: 1.5 }); got <nil>".
func TestAppHistoryDays(t *testing.T) {
	for _, test := range []struct {
		body string
		want int
	}{
		{`app.history({ days: 90 });`, 90},
		{`app.history({ days: 1 });`, 1},
		{``, 0},
	} {
		raw, err := evalConfigJS(writeApplication(t, test.body, nil))
		if err != nil {
			t.Fatalf("%s: %v", test.body, err)
		}
		cfg, err := ParseConfig(raw)
		if err != nil {
			t.Fatalf("%s: %v", test.body, err)
		}
		if cfg.HistoryDays != test.want {
			t.Errorf("%s keeps %d days, want %d", test.body, cfg.HistoryDays, test.want)
		}
	}
}

func TestAppHistoryRefusals(t *testing.T) {
	for _, test := range []struct{ body, want string }{
		{`app.history(90);`, "app.history expects one object: app.history({ days: 90 })"},
		{`app.history({});`, "app.history needs days"},
		{`app.history({ days: 0 });`, "days is a whole number of days from 1 to 36500"},
		{`app.history({ days: 1.5 });`, "days is a whole number of days from 1 to 36500"},
		{`app.history({ days: "90" });`, "days is a whole number of days from 1 to 36500"},
		{`app.history({ weeks: 2 });`, "weeks is not a setting of the history; it has days"},
		{`app.history({ days: 30 });` + "\n" + `app.history({ days: 90 });`, "app.history is declared twice"},
	} {
		_, err := evalConfigJS(writeApplication(t, test.body, nil))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s\n  got  %v\n  want …%s…", test.body, err, test.want)
		}
	}
}
