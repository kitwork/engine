// Package env reads .env files and turns their values into Kitwork values (docs/KITWORK_ENVIRONMENT.md
// mục 4): a value in its canonical form becomes a number or a boolean, a quoted one is always a string.
package env

import (
	"bufio"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// Entry is one value of a .env file: its text, the quotes taken off, and whether the author quoted it
// ("0123", '1.10'), which keeps it a string whatever it looks like. A value of the operating system is
// never quoted.
type Entry struct {
	Text   string
	Quoted bool
}

// Present reports a value that counts: an empty one (KEY=, KEY="") is no value (mục 4).
func (e Entry) Present() bool { return e.Text != "" }

// ParseEntries reads KEY=VALUE lines: blank lines and `#` comments are skipped, an `export ` before a key
// is allowed, and "double" or 'single' quotes around a value are taken off and remembered.
func ParseEntries(source io.Reader) map[string]Entry {
	vars := map[string]Entry{}
	scanner := bufio.NewScanner(source)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		entry := Entry{Text: strings.TrimSpace(line[eq+1:])}
		if text := entry.Text; len(text) >= 2 &&
			((text[0] == '"' && text[len(text)-1] == '"') || (text[0] == '\'' && text[len(text)-1] == '\'')) {
			entry = Entry{Text: text[1 : len(text)-1], Quoted: true}
		}
		if key != "" {
			vars[key] = entry
		}
	}
	return vars
}

// ParseDotEnvEntries reads the .env file at path (ParseEntries). A missing file is no error: it holds
// nothing.
func ParseDotEnvEntries(path string) map[string]Entry {
	file, err := os.Open(path)
	if err != nil {
		return map[string]Entry{}
	}
	defer file.Close()
	return ParseEntries(file)
}

// ParseDotEnv is ParseDotEnvEntries as text alone, for a reader that wants the words as written (the
// host's MINIFY).
func ParseDotEnv(path string) map[string]string {
	entries := ParseDotEnvEntries(path)
	vars := make(map[string]string, len(entries))
	for key, entry := range entries {
		vars[key] = entry.Text
	}
	return vars
}

// CoerceValue is the value of an entry (mục 4): a quoted one is a string; true and false in any case are
// booleans; a number only in its canonical form, the one writing the number out again gives back letter
// for letter (42, -7, 3.14, -0.5). 0123, +84912345678, 1e3, 1.10, -0, Infinity, NaN and a number too long
// for a float to hold exactly stay strings, so a postal code, a phone number or a version never loses a
// zero or a last digit.
func CoerceValue(entry Entry) value.Value {
	text := entry.Text
	if entry.Quoted {
		return value.NewString(text)
	}
	switch strings.ToLower(text) {
	case "true":
		return value.New(true)
	case "false":
		return value.New(false)
	}
	if number, ok := canonicalNumber(text); ok {
		return value.New(number)
	}
	return value.NewString(text)
}

// canonicalNumber is the number text writes when text is that number's own form.
func canonicalNumber(text string) (float64, bool) {
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || (number == 0 && math.Signbit(number)) {
		return 0, false
	}
	return number, strconv.FormatFloat(number, 'f', -1, 64) == text
}
