package work

import (
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

func TestParseKitDBRecoveryOptionKeepsBooleanCompatibility(t *testing.T) {
	retain, policy, err := parseKitDBRecoveryOption(value.New(true))
	if err != nil || !retain || policy.Enabled() {
		t.Fatalf("recovery true = (%t, %#v, %v)", retain, policy, err)
	}
	retain, policy, err = parseKitDBRecoveryOption(value.New(false))
	if err != nil || retain || policy.Enabled() {
		t.Fatalf("recovery false = (%t, %#v, %v)", retain, policy, err)
	}
}

func TestKitDBSearchHistoryDefaultHonorsExplicitRecoveryFalse(t *testing.T) {
	searchable := map[string]map[string]*ColumnSpec{
		"products": {"name": {kind: "text", searchable: true}},
	}
	implicit := &dbProxy{engine: "kitdb", tables: searchable}
	implicit.enableSearchHistoryByDefault()
	if !implicit.retainHistory {
		t.Fatal("searchable KitDB did not enable retained history")
	}

	explicit := &dbProxy{
		engine: "kitdb", tables: searchable, recoverySet: true, retainHistory: false,
	}
	explicit.enableSearchHistoryByDefault()
	if explicit.retainHistory {
		t.Fatal("explicit recovery:false was overridden")
	}

	sqlite := &dbProxy{engine: "sqlite", tables: searchable}
	sqlite.enableSearchHistoryByDefault()
	if sqlite.retainHistory {
		t.Fatal("SQLite search unexpectedly enabled KitDB history")
	}
}

func TestParseKitDBRecoveryOptionReadsBoundedPolicy(t *testing.T) {
	retain, policy, err := parseKitDBRecoveryOption(value.New(map[string]value.Value{
		"maxAge":   value.New("7d"),
		"maxBytes": value.New("2gb"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !retain || policy.MaxAge != 7*24*time.Hour || policy.MaxBytes != 2<<30 {
		t.Fatalf("bounded recovery = (%t, %#v)", retain, policy)
	}
}

func TestParseKitDBRecoveryOptionRejectsTyposAndUnsafeBounds(t *testing.T) {
	tests := []struct {
		name string
		raw  value.Value
		want string
	}{
		{
			name: "empty",
			raw:  value.New(map[string]value.Value{}),
			want: "requires maxAge or maxBytes",
		},
		{
			name: "typo",
			raw: value.New(map[string]value.Value{
				"maxByte": value.New("2gb"),
			}),
			want: "unknown option",
		},
		{
			name: "fractional bytes",
			raw: value.New(map[string]value.Value{
				"maxBytes": value.New(1.5),
			}),
			want: "positive whole number",
		},
		{
			name: "zero age",
			raw: value.New(map[string]value.Value{
				"maxAge": value.New("0s"),
			}),
			want: "positive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := parseKitDBRecoveryOption(test.raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parse error = %v, want %q", err, test.want)
			}
		})
	}
}
