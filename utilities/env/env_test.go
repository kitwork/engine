package env_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	envhelper "github.com/kitwork/engine/utilities/env"
	"github.com/kitwork/engine/value"
)

func TestParseDotEnv(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "env_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	envFile := filepath.Join(tmpDir, ".env")
	content := []byte(`# comment
PORT=8080
export HOST="localhost"
ALLOW_LOCAL=true
PI=3.14
`)
	if err := os.WriteFile(envFile, content, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	vars := envhelper.ParseDotEnv(envFile)
	if vars["PORT"] != "8080" {
		t.Errorf("Expected PORT=8080, got %q", vars["PORT"])
	}
	if vars["HOST"] != "localhost" {
		t.Errorf("Expected HOST=localhost, got %q", vars["HOST"])
	}
	if vars["ALLOW_LOCAL"] != "true" {
		t.Errorf("Expected ALLOW_LOCAL=true, got %q", vars["ALLOW_LOCAL"])
	}
	if entries := envhelper.ParseDotEnvEntries(envFile); !entries["HOST"].Quoted || entries["PORT"].Quoted {
		t.Errorf("quoted HOST = %v, unquoted PORT = %v: the quotes are remembered", entries["HOST"], entries["PORT"])
	}
}

// A value becomes a number only in its canonical form, the one writing the number out again gives back
// (docs/KITWORK_ENVIRONMENT.md mục 4, 22/10/2026); true and false in any case are booleans; anything quoted
// is a string; an empty value is no value. A postal code, a phone number, a version keep their zeros and
// their last digits.
//
// CONTROL (22/10/2026): with the coercion of before put back (strconv.Atoi, then strconv.ParseFloat, the
// quotes ignored), twelve rows were red: 0123 gave the number 123, +84912345678 84912345678, 1e3 1000, 1.10
// 1.1, -0 0, 9007199254740993 9007199254740992, Infinity and NaN numbers, and "0123", '1.10', "42" numbers,
// "true" a boolean.
func TestCoercionKeepsCanonicalForms(t *testing.T) {
	source := strings.Join([]string{
		"INT=42", "NEGATIVE=-7", "ZERO=0", "DECIMAL=3.14", "NEGATIVE_DECIMAL=-0.5",
		"UPPER_TRUE=TRUE", "LOWER_FALSE=false", "MIXED=True",
		"ZIP=0123", "PHONE=+84912345678", "EXPONENT=1e3", "VERSION=1.10", "INFINITY=Infinity", "NAN=NaN",
		"NEGATIVE_ZERO=-0", "LONG=9007199254740993", "WORD=kit",
		`QUOTED_ZIP="0123"`, `SINGLE_VERSION='1.10'`, `QUOTED_TRUE="true"`, `QUOTED_NUMBER="42"`,
		"EMPTY=", `QUOTED_EMPTY=""`,
	}, "\n")
	entries := envhelper.ParseEntries(strings.NewReader(source))
	number := func(n float64) value.Value { return value.New(n) }
	text := func(s string) value.Value { return value.NewString(s) }
	for key, want := range map[string]value.Value{
		"INT": number(42), "NEGATIVE": number(-7), "ZERO": number(0), "DECIMAL": number(3.14), "NEGATIVE_DECIMAL": number(-0.5),
		"UPPER_TRUE": value.New(true), "LOWER_FALSE": value.New(false), "MIXED": value.New(true),
		"ZIP": text("0123"), "PHONE": text("+84912345678"), "EXPONENT": text("1e3"), "VERSION": text("1.10"),
		"INFINITY": text("Infinity"), "NAN": text("NaN"), "NEGATIVE_ZERO": text("-0"), "LONG": text("9007199254740993"),
		"WORD":       text("kit"),
		"QUOTED_ZIP": text("0123"), "SINGLE_VERSION": text("1.10"), "QUOTED_TRUE": text("true"), "QUOTED_NUMBER": text("42"),
	} {
		got := envhelper.CoerceValue(entries[key])
		if got.K != want.K || got.Text() != want.Text() {
			t.Errorf("%s=%q (quoted %v) = %s %q, want %s %q", key, entries[key].Text, entries[key].Quoted, kind(got), got.Text(), kind(want), want.Text())
		}
	}
	for _, key := range []string{"EMPTY", "QUOTED_EMPTY"} {
		if entry, ok := entries[key]; !ok || entry.Present() {
			t.Errorf("%s = %+v (read %v), want read and no value", key, entry, ok)
		}
	}
}

func kind(v value.Value) string {
	switch v.K {
	case value.Number:
		return "number"
	case value.Bool:
		return "boolean"
	case value.String:
		return "string"
	}
	return "other"
}
