// Package env provides .env file parsing and automatic type coercion for environment variables.
package env

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// ParseDotEnv reads KEY=VALUE pairs from a .env file into a map. A missing file
// is NOT an error (returns an empty map). Supports blank lines, `#` comments,
// an optional `export ` prefix, and surrounding "double"/'single' quotes.
func ParseDotEnv(path string) map[string]string {
	vars := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return vars
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key != "" {
			vars[key] = val
		}
	}
	return vars
}

// Coerce converts an environment string value to its natural Go value: bool > int > float > string.
func Coerce(v string) any {
	t := strings.TrimSpace(v)
	switch strings.ToLower(t) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(t); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return f
	}
	return v
}

// CoerceValue converts an environment string to a value.Value representation.
func CoerceValue(v string) value.Value {
	t := strings.TrimSpace(v)
	switch strings.ToLower(t) {
	case "true":
		return value.New(true)
	case "false":
		return value.New(false)
	}
	if n, err := strconv.Atoi(t); err == nil {
		return value.New(n)
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil {
		return value.New(f)
	}
	return value.NewString(v)
}
