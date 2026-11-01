package gateway

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kitwork/engine/search"
	sqlite "modernc.org/sqlite"
)

// PostgreSQL's functions a database tool calls that SQLite does not have, registered with the SQLite driver under
// kitwork_pg_… names (translate.go renames a call of format_type to kitwork_pg_format_type, …), so no SQLite
// connection of the engine sees a function under PostgreSQL's name. Arrays are PostgreSQL's text form ({a,b}, or
// "1 2" for int2vector), as the catalog stores them and the client reads them; kitwork_pg_json turns one into the JSON
// array json_each reads.

var startedAt = time.Now().UTC()

func init() {
	scalar := func(name string, arguments int32, function func(arguments []driver.Value) (driver.Value, error)) {
		sqlite.MustRegisterFunction(name, &sqlite.FunctionImpl{NArgs: arguments, Deterministic: true,
			Scalar: func(_ *sqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
				return function(arguments)
			}})
	}
	constant := func(value driver.Value) func([]driver.Value) (driver.Value, error) {
		return func([]driver.Value) (driver.Value, error) { return value, nil }
	}
	scalar("kitwork_pg_null", -1, constant(nil))
	scalar("kitwork_pg_true", -1, constant(int64(1)))
	scalar("kitwork_pg_false", -1, constant(int64(0)))
	scalar("kitwork_pg_empty", -1, constant(""))
	scalar("kitwork_pg_first", -1, func(arguments []driver.Value) (driver.Value, error) {
		if len(arguments) == 0 {
			return nil, nil
		}
		return arguments[0], nil
	})
	scalar("kitwork_pg_format_type", 2, func(arguments []driver.Value) (driver.Value, error) {
		oid, ok := integerValue(arguments[0])
		if !ok {
			return nil, nil
		}
		modifier, hasModifier := integerValue(arguments[1])
		return formatType(oid, modifier, hasModifier), nil
	})
	scalar("kitwork_pg_encoding_to_char", 1, func(arguments []driver.Value) (driver.Value, error) {
		if code, ok := integerValue(arguments[0]); ok && code == encodingUTF8 {
			return "UTF8", nil
		}
		return "", nil
	})
	scalar("kitwork_pg_size_pretty", 1, func(arguments []driver.Value) (driver.Value, error) {
		size, ok := integerValue(arguments[0])
		if !ok {
			return nil, nil
		}
		return sizePretty(size), nil
	})
	scalar("kitwork_pg_quote_ident", 1, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil {
			return nil, nil
		}
		return quoteName(textValue(arguments[0])), nil
	})
	scalar("kitwork_pg_quote_literal", 1, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil {
			return nil, nil
		}
		return pgQuoteLiteral(textValue(arguments[0])), nil
	})
	scalar("kitwork_pg_quote_nullable", 1, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil {
			return "NULL", nil
		}
		return pgQuoteLiteral(textValue(arguments[0])), nil
	})
	scalar("kitwork_pg_regexp_replace", -1, func(arguments []driver.Value) (driver.Value, error) {
		if len(arguments) < 3 || len(arguments) > 4 {
			return nil, fmt.Errorf("regexp_replace takes 3 or 4 arguments")
		}
		for _, argument := range arguments[:3] {
			if argument == nil {
				return nil, nil
			}
		}
		flags := ""
		if len(arguments) == 4 {
			flags = textValue(arguments[3])
		}
		expression, err := compilePattern(textValue(arguments[1]), flags)
		if err != nil {
			return nil, err
		}
		replacement := pgReplacement(textValue(arguments[2]))
		subject := textValue(arguments[0])
		if strings.Contains(flags, "g") {
			return expression.ReplaceAllString(subject, replacement), nil
		}
		location := expression.FindStringSubmatchIndex(subject)
		if location == nil {
			return subject, nil
		}
		var result []byte
		result = append(result, subject[:location[0]]...)
		result = expression.ExpandString(result, replacement, subject, location)
		return string(append(result, subject[location[1]:]...)), nil
	})
	scalar("kitwork_pg_regexp", 3, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil || arguments[1] == nil {
			return nil, nil
		}
		expression, err := compilePattern(textValue(arguments[1]), textValue(arguments[2]))
		if err != nil {
			return nil, err
		}
		return boolValue(expression.MatchString(textValue(arguments[0]))), nil
	})
	scalar("kitwork_pg_current_setting", -1, func(arguments []driver.Value) (driver.Value, error) {
		if len(arguments) == 0 || arguments[0] == nil {
			return nil, nil
		}
		key := strings.ToUpper(textValue(arguments[0]))
		if key == "TIME ZONE" {
			key = "TIMEZONE"
		}
		if setting, known := settings[key]; known {
			return setting[1], nil
		}
		if len(arguments) > 1 {
			if missing, ok := integerValue(arguments[1]); ok && missing != 0 {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("unrecognized configuration parameter %q", textValue(arguments[0]))
	})
	scalar("kitwork_pg_set_config", 3, func(arguments []driver.Value) (driver.Value, error) {
		return arguments[1], nil
	})
	scalar("kitwork_pg_backend_pid", 0, constant(int64(os.Getpid())))
	scalar("kitwork_pg_start_time", 0, constant(startedAt.Format(time.RFC3339Nano)))
	scalar("kitwork_pg_bool", 1, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil {
			return nil, nil
		}
		text, ok := booleanText(arguments[0])
		if !ok {
			return nil, fmt.Errorf("invalid input syntax for type boolean: %q", textValue(arguments[0]))
		}
		return boolValue(text == "t"), nil
	})
	scalar("kitwork_pg_object_key", 1, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil {
			return nil, nil
		}
		parts := splitObjectName(textValue(arguments[0]))
		if len(parts) == 1 {
			return parts[0], nil
		}
		return parts[len(parts)-2] + "." + parts[len(parts)-1], nil
	})
	scalar("kitwork_pg_type_oid", 1, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil {
			return nil, nil
		}
		if typ, known := pgTypesByName[typeName(textValue(arguments[0]))]; known {
			return int64(typ.oid), nil
		}
		return nil, nil
	})
	scalar("kitwork_pg_array", -1, func(arguments []driver.Value) (driver.Value, error) {
		elements := make([]arrayElement, len(arguments))
		for index, argument := range arguments {
			elements[index] = arrayElement{text: textValue(argument), null: argument == nil}
		}
		return formatArray(elements), nil
	})
	scalar("kitwork_pg_array_text", 1, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil {
			return nil, nil
		}
		elements, ok := parseArray(textValue(arguments[0]))
		if !ok {
			return nil, fmt.Errorf("malformed array literal: %q", textValue(arguments[0]))
		}
		return formatArray(elements), nil
	})
	scalar("kitwork_pg_json", 1, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil {
			return "[]", nil
		}
		elements, ok := parseArray(textValue(arguments[0]))
		if !ok {
			return nil, fmt.Errorf("malformed array literal: %q", textValue(arguments[0]))
		}
		values := make([]any, len(elements))
		for index, element := range elements {
			switch {
			case element.null:
				values[index] = nil
			case element.number:
				values[index] = json.Number(element.text)
			default:
				values[index] = element.text
			}
		}
		encoded, err := json.Marshal(values)
		return string(encoded), err
	})
	scalar("kitwork_pg_subscript", 2, func(arguments []driver.Value) (driver.Value, error) {
		position, ok := integerValue(arguments[1])
		if arguments[0] == nil || !ok {
			return nil, nil
		}
		text := textValue(arguments[0])
		elements, ok := parseArray(text)
		if !ok {
			return nil, nil
		}
		// A vector (int2vector, oidvector: "1 2") counts from 0, an array ({…}) from 1.
		if strings.HasPrefix(strings.TrimSpace(text), "{") || strings.HasPrefix(strings.TrimSpace(text), "[") {
			position--
		}
		if position < 0 || position >= int64(len(elements)) || elements[position].null {
			return nil, nil
		}
		return elementValue(elements[position]), nil
	})
	scalar("kitwork_pg_array_to_string", -1, func(arguments []driver.Value) (driver.Value, error) {
		if len(arguments) < 2 || arguments[0] == nil || arguments[1] == nil {
			return nil, nil
		}
		elements, ok := parseArray(textValue(arguments[0]))
		if !ok {
			return nil, nil
		}
		var parts []string
		for _, element := range elements {
			switch {
			case !element.null:
				parts = append(parts, element.text)
			case len(arguments) > 2 && arguments[2] != nil:
				parts = append(parts, textValue(arguments[2]))
			}
		}
		return strings.Join(parts, textValue(arguments[1])), nil
	})
	scalar("kitwork_pg_array_length", 2, func(arguments []driver.Value) (driver.Value, error) {
		elements, ok := parseArray(textValue(arguments[0]))
		if arguments[0] == nil || !ok || len(elements) == 0 {
			return nil, nil
		}
		return int64(len(elements)), nil
	})
	scalar("kitwork_pg_array_lower", 2, func(arguments []driver.Value) (driver.Value, error) {
		elements, ok := parseArray(textValue(arguments[0]))
		if arguments[0] == nil || !ok || len(elements) == 0 {
			return nil, nil
		}
		return int64(1), nil
	})
	scalar("kitwork_pg_cardinality", 1, func(arguments []driver.Value) (driver.Value, error) {
		elements, ok := parseArray(textValue(arguments[0]))
		if arguments[0] == nil || !ok {
			return nil, nil
		}
		return int64(len(elements)), nil
	})
	scalar("kitwork_pg_string_to_array", -1, func(arguments []driver.Value) (driver.Value, error) {
		if len(arguments) < 2 || arguments[0] == nil {
			return nil, nil
		}
		text := textValue(arguments[0])
		var parts []string
		if arguments[1] == nil {
			for _, character := range text {
				parts = append(parts, string(character))
			}
		} else if separator := textValue(arguments[1]); separator == "" {
			parts = []string{text}
		} else {
			parts = strings.Split(text, separator)
		}
		elements := make([]arrayElement, len(parts))
		for index, part := range parts {
			elements[index] = arrayElement{text: part, null: len(arguments) > 2 && arguments[2] != nil && part == textValue(arguments[2])}
		}
		return formatArray(elements), nil
	})
	sqlite.MustRegisterFunction("kitwork_pg_now", &sqlite.FunctionImpl{NArgs: 0,
		Scalar: func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
			return time.Now().UTC().Format("2006-01-02 15:04:05.999999Z07:00"), nil
		}})
	scalar("kitwork_pg_split_part", 3, func(arguments []driver.Value) (driver.Value, error) {
		position, ok := integerValue(arguments[2])
		if arguments[0] == nil || arguments[1] == nil || !ok {
			return nil, nil
		}
		parts := strings.Split(textValue(arguments[0]), textValue(arguments[1]))
		if position < 0 {
			position += int64(len(parts)) + 1
		}
		if position < 1 || position > int64(len(parts)) {
			return "", nil
		}
		return parts[position-1], nil
	})
	scalar("kitwork_pg_left", 2, func(arguments []driver.Value) (driver.Value, error) {
		return cut(arguments, true), nil
	})
	scalar("kitwork_pg_right", 2, func(arguments []driver.Value) (driver.Value, error) {
		return cut(arguments, false), nil
	})
	scalar("kitwork_pg_lpad", -1, func(arguments []driver.Value) (driver.Value, error) {
		return pad(arguments, true), nil
	})
	scalar("kitwork_pg_rpad", -1, func(arguments []driver.Value) (driver.Value, error) {
		return pad(arguments, false), nil
	})
	scalar("kitwork_pg_initcap", 1, func(arguments []driver.Value) (driver.Value, error) {
		if arguments[0] == nil {
			return nil, nil
		}
		runes := []rune(strings.ToLower(textValue(arguments[0])))
		start := true
		for index, character := range runes {
			if unicode.IsLetter(character) || unicode.IsDigit(character) {
				if start {
					runes[index] = unicode.ToUpper(character)
				}
				start = false
			} else {
				start = true
			}
		}
		return string(runes), nil
	})
	sqlite.MustRegisterFunction("kitwork_pg_array_agg", &sqlite.FunctionImpl{NArgs: 1,
		MakeAggregate: func(sqlite.FunctionContext) (sqlite.AggregateFunction, error) { return &arrayAggregate{}, nil }})
	registerMatchFilters(scalar)
	// kitwork_snippet(words, text, weight, text, weight, …) is MATCH's _snippet_ (search.go).
	scalar("kitwork_snippet", -1, func(arguments []driver.Value) (driver.Value, error) {
		if len(arguments)%2 != 1 {
			return nil, fmt.Errorf("kitwork_snippet takes the words, then a text and a weight for each column")
		}
		texts := make([]snippetText, 0, len(arguments)/2)
		for index := 1; index+1 < len(arguments); index += 2 {
			weight, _ := arguments[index+1].(float64)
			if number, integer := arguments[index+1].(int64); integer {
				weight = float64(number)
			}
			texts = append(texts, snippetText{text: textValue(arguments[index]), weight: weight})
		}
		return snippet(textValue(arguments[0]), texts)
	})
}

// matchTerms are the terms text gives MATCH's index: its words without accents or case.
func matchTerms(text string) ([]string, error) {
	var terms []string
	err := search.VietnameseAnalyzer().Analyze(context.Background(), text, func(token search.Token) bool {
		terms = append(terms, token.Term)
		return true
	})
	return terms, err
}

// matchHas reports whether any of texts has a term that accept takes.
func matchHas(texts []driver.Value, accept func(string) bool) (bool, error) {
	for _, value := range texts {
		terms, err := matchTerms(textValue(value))
		if err != nil {
			return false, err
		}
		for _, term := range terms {
			if accept(term) {
				return true, nil
			}
		}
	}
	return false, nil
}

// registerMatchFilters registers what MATCH checks on the rows its index ranked: kitwork_match_word(word, text, …),
// a word left out (NOT co), true when a text has every term of word; kitwork_match_prefix(prefix, text, …), a last
// word typed in part (quan dui na*), true when a text has a term beginning so.
func registerMatchFilters(scalar func(string, int32, func([]driver.Value) (driver.Value, error))) {
	scalar("kitwork_match_word", -1, func(arguments []driver.Value) (driver.Value, error) {
		if len(arguments) < 2 {
			return nil, fmt.Errorf("kitwork_match_word takes a word and texts")
		}
		wanted, err := matchTerms(textValue(arguments[0]))
		if err != nil || len(wanted) == 0 {
			return boolValue(false), err
		}
		for _, term := range wanted {
			found, err := matchHas(arguments[1:], func(candidate string) bool { return candidate == term })
			if err != nil || !found {
				return boolValue(false), err
			}
		}
		return boolValue(true), nil
	})
	scalar("kitwork_match_prefix", -1, func(arguments []driver.Value) (driver.Value, error) {
		if len(arguments) < 2 {
			return nil, fmt.Errorf("kitwork_match_prefix takes a prefix and texts")
		}
		stems, err := matchTerms(textValue(arguments[0]))
		if err != nil || len(stems) == 0 {
			return boolValue(false), err
		}
		found, err := matchHas(arguments[1:], func(candidate string) bool { return strings.HasPrefix(candidate, stems[0]) })
		return boolValue(found), err
	})
}

// Snippet bounds, as the KitDB port cut its _snippet: about 18 words, 4 of them around the first match.
const (
	snippetTokens  = 18
	snippetContext = 4
)

type snippetText struct {
	text   string
	weight float64
}

// snippet is the passage of the column that matches best (its matched words times its weight; the first column
// with text when none matches) around the words, HTML-escaped, each matched word in <b>…</b>, "..." where the column
// goes on.
func snippet(words string, texts []snippetText) (string, error) {
	var best search.Fragment
	bestQuality := -1.0
	for _, candidate := range texts {
		if candidate.text == "" {
			continue
		}
		fragment, err := search.Highlight(context.Background(), search.VietnameseAnalyzer(), candidate.text, words,
			search.FragmentOptions{MaxTokens: snippetTokens, ContextTokens: snippetContext})
		if err != nil {
			return "", fmt.Errorf("snippet: %w", err)
		}
		if quality := float64(len(fragment.Matches)) * candidate.weight; quality > bestQuality {
			best, bestQuality = fragment, quality
		}
	}
	if best.Text == "" {
		return "", nil
	}
	var output strings.Builder
	if best.PrefixElided {
		output.WriteString("... ")
	}
	cursor := 0
	for _, match := range best.Matches {
		if match.Start < cursor || match.End < match.Start || match.End > len(best.Text) {
			continue
		}
		output.WriteString(html.EscapeString(best.Text[cursor:match.Start]))
		output.WriteString("<b>" + html.EscapeString(best.Text[match.Start:match.End]) + "</b>")
		cursor = match.End
	}
	output.WriteString(html.EscapeString(best.Text[cursor:]))
	if best.SuffixElided {
		output.WriteString(" ...")
	}
	return output.String(), nil
}

func integerValue(value driver.Value) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case string:
		number, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return number, err == nil
	case []byte:
		number, err := strconv.ParseInt(strings.TrimSpace(string(typed)), 10, 64)
		return number, err == nil
	}
	return 0, false
}

func textValue(value driver.Value) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	}
	return plainText(value)
}

func boolValue(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func sizePretty(size int64) string {
	magnitude := size
	if magnitude < 0 {
		magnitude = -magnitude
	}
	units := []string{"bytes", "kB", "MB", "GB", "TB", "PB"}
	unit := 0
	for magnitude >= 10*1024 && unit < len(units)-1 {
		magnitude = (magnitude + 512) / 1024
		unit++
	}
	if size < 0 {
		magnitude = -magnitude
	}
	return strconv.FormatInt(magnitude, 10) + " " + units[unit]
}

// pgKeywords are the words quote_ident quotes although they are plain: PostgreSQL's reserved keywords.
var pgKeywords = map[string]bool{
	"all": true, "analyse": true, "analyze": true, "and": true, "any": true, "array": true, "as": true, "asc": true,
	"asymmetric": true, "both": true, "case": true, "cast": true, "check": true, "collate": true, "column": true,
	"constraint": true, "create": true, "current_catalog": true, "current_date": true, "current_role": true,
	"current_time": true, "current_timestamp": true, "current_user": true, "default": true, "deferrable": true,
	"desc": true, "distinct": true, "do": true, "else": true, "end": true, "except": true, "false": true,
	"fetch": true, "for": true, "foreign": true, "from": true, "grant": true, "group": true, "having": true, "in": true,
	"initially": true, "intersect": true, "into": true, "lateral": true, "leading": true, "limit": true,
	"localtime": true, "localtimestamp": true, "not": true, "null": true, "offset": true, "on": true, "only": true,
	"or": true, "order": true, "placing": true, "primary": true, "references": true, "returning": true,
	"select": true, "session_user": true, "some": true, "symmetric": true, "system_user": true, "table": true,
	"then": true, "to": true, "trailing": true, "true": true, "union": true, "unique": true, "user": true,
	"using": true, "variadic": true, "when": true, "where": true, "window": true, "with": true,
}

// pgPlainName reports a name PostgreSQL writes without quotes: lower-case letters, digits, '_' and '$', not starting
// with a digit or '$', and not a reserved keyword.
func pgPlainName(name string) bool {
	if name == "" || pgKeywords[name] {
		return false
	}
	for index, character := range name {
		switch {
		case character >= 'a' && character <= 'z', character == '_':
		case (character >= '0' && character <= '9') || character == '$':
			if index == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func pgQuoteLiteral(value string) string {
	if strings.Contains(value, `\`) {
		return "E'" + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), "'", "''") + "'"
	}
	return quoteLiteral(value)
}

// patterns caches compiled regular expressions; it is emptied when it reaches its bound.
var patterns = struct {
	sync.Mutex
	compiled map[string]*regexp.Regexp
}{compiled: map[string]*regexp.Regexp{}}

const maxPatterns = 256

// compilePattern compiles a PostgreSQL regular expression with its flags (i: ignore case; PostgreSQL's '.' matches a
// newline unless n is given).
func compilePattern(pattern, flags string) (*regexp.Regexp, error) {
	prefix := "(?s)"
	if strings.Contains(flags, "n") {
		prefix = "(?m)"
	}
	if strings.Contains(flags, "i") {
		prefix += "(?i)"
	}
	key := prefix + pattern
	patterns.Lock()
	defer patterns.Unlock()
	if expression, known := patterns.compiled[key]; known {
		return expression, nil
	}
	expression, err := regexp.Compile(key)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression: %v", err)
	}
	if len(patterns.compiled) >= maxPatterns {
		patterns.compiled = map[string]*regexp.Regexp{}
	}
	patterns.compiled[key] = expression
	return expression, nil
}

// pgReplacement turns regexp_replace's replacement (\1 a group, \& the match, \\ a backslash) into Go's ($1, $0, $$).
func pgReplacement(replacement string) string {
	var builder strings.Builder
	for index := 0; index < len(replacement); index++ {
		character := replacement[index]
		switch {
		case character == '$':
			builder.WriteString("$$")
		case character == '\\' && index+1 < len(replacement):
			next := replacement[index+1]
			switch {
			case next >= '1' && next <= '9':
				builder.WriteString("${" + string(next) + "}")
			case next == '&':
				builder.WriteString("${0}")
			default:
				builder.WriteByte(next)
			}
			index++
		default:
			builder.WriteByte(character)
		}
	}
	return builder.String()
}

// splitObjectName splits "schema.name", "\"Schema\".\"Name\"" or "name" into its parts, unquoted (a bare part is
// folded to lower case, as PostgreSQL folds it).
func splitObjectName(value string) []string {
	var parts []string
	var current strings.Builder
	quoted, wasQuoted := false, false
	flush := func() {
		part := current.String()
		if !wasQuoted {
			part = strings.ToLower(strings.TrimSpace(part))
		}
		parts = append(parts, part)
		current.Reset()
		wasQuoted = false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character == '"' && quoted && index+1 < len(value) && value[index+1] == '"':
			current.WriteByte('"')
			index++
		case character == '"':
			quoted = !quoted
			wasQuoted = true
		case character == '.' && !quoted:
			flush()
		default:
			current.WriteByte(character)
		}
	}
	flush()
	return parts
}

// typeAliases are the SQL names of PostgreSQL's types, by pg_type's typname.
var typeAliases = map[string]string{
	"integer": "int4", "int": "int4", "bigint": "int8", "smallint": "int2", "boolean": "bool", "real": "float4",
	"double precision": "float8", "float": "float8", "character varying": "varchar", "character": "bpchar",
	"char": "bpchar", "decimal": "numeric", "timestamp without time zone": "timestamp",
	"timestamp with time zone": "timestamptz", "time without time zone": "time", `"char"`: "char",
}

// typeName is the typname of a type a client names: 'integer', 'pg_catalog.int4', 'character varying(20)', 'text[]'.
func typeName(value string) string {
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.TrimPrefix(name, "pg_catalog.")
	array := strings.HasSuffix(name, "[]")
	name = strings.TrimSuffix(name, "[]")
	if cut := strings.IndexByte(name, '('); cut >= 0 {
		name = strings.TrimSpace(name[:cut] + name[strings.IndexByte(name, ')')+1:])
	}
	if alias, known := typeAliases[name]; known {
		name = alias
	}
	if array {
		name = "_" + name
	}
	return name
}

// arrayElement is one element of an array in PostgreSQL's text form.
type arrayElement struct {
	text   string
	null   bool
	number bool // written without quotes and reads as a number
}

func elementValue(element arrayElement) driver.Value {
	if element.number {
		if number, err := strconv.ParseInt(element.text, 10, 64); err == nil {
			return number
		}
		if number, err := strconv.ParseFloat(element.text, 64); err == nil {
			return number
		}
	}
	return element.text
}

// parseArray reads an array written as PostgreSQL does ({a,"b c",NULL}), as a vector ("1 2"), or as JSON ([1,"a"]).
func parseArray(text string) ([]arrayElement, bool) {
	trimmed := strings.TrimSpace(text)
	switch {
	case trimmed == "":
		return nil, true
	case strings.HasPrefix(trimmed, "["):
		var values []any
		decoder := json.NewDecoder(strings.NewReader(trimmed))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return nil, false
		}
		elements := make([]arrayElement, len(values))
		for index, value := range values {
			switch typed := value.(type) {
			case nil:
				elements[index] = arrayElement{null: true}
			case json.Number:
				elements[index] = arrayElement{text: typed.String(), number: true}
			case string:
				elements[index] = arrayElement{text: typed}
			default:
				encoded, _ := json.Marshal(typed)
				elements[index] = arrayElement{text: string(encoded)}
			}
		}
		return elements, true
	case !strings.HasPrefix(trimmed, "{"):
		var elements []arrayElement
		for _, field := range strings.Fields(trimmed) {
			elements = append(elements, arrayElement{text: field, number: isNumber(field)})
		}
		return elements, true
	}
	inner := trimmed[1:]
	if !strings.HasSuffix(inner, "}") {
		return nil, false
	}
	inner = inner[:len(inner)-1]
	var elements []arrayElement
	if strings.TrimSpace(inner) == "" {
		return elements, true
	}
	for index := 0; index <= len(inner); {
		for index < len(inner) && inner[index] == ' ' {
			index++
		}
		if index < len(inner) && inner[index] == '"' {
			var builder strings.Builder
			index++
			for index < len(inner) && inner[index] != '"' {
				if inner[index] == '\\' && index+1 < len(inner) {
					index++
				}
				builder.WriteByte(inner[index])
				index++
			}
			if index >= len(inner) {
				return nil, false
			}
			index++
			elements = append(elements, arrayElement{text: builder.String()})
		} else {
			end := index
			for end < len(inner) && inner[end] != ',' {
				end++
			}
			field := strings.TrimSpace(inner[index:end])
			if strings.EqualFold(field, "NULL") {
				elements = append(elements, arrayElement{null: true})
			} else {
				elements = append(elements, arrayElement{text: field, number: isNumber(field)})
			}
			index = end
		}
		for index < len(inner) && inner[index] == ' ' {
			index++
		}
		if index >= len(inner) {
			break
		}
		if inner[index] != ',' {
			return nil, false
		}
		index++
	}
	return elements, true
}

func isNumber(text string) bool {
	_, err := strconv.ParseFloat(text, 64)
	return err == nil && text != "" && !strings.ContainsAny(text, "xXnN")
}

// formatArray writes elements in PostgreSQL's text form, quoting an element that needs it.
func formatArray(elements []arrayElement) string {
	parts := make([]string, len(elements))
	for index, element := range elements {
		switch {
		case element.null:
			parts[index] = "NULL"
		case element.text == "" || strings.EqualFold(element.text, "NULL") || strings.ContainsAny(element.text, "{},\"\\ \t\n"):
			parts[index] = `"` + strings.ReplaceAll(strings.ReplaceAll(element.text, `\`, `\\`), `"`, `\"`) + `"`
		default:
			parts[index] = element.text
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// pgArray is an array of plain elements (numbers, names) in PostgreSQL's text form.
func pgArray(values []string) string {
	elements := make([]arrayElement, len(values))
	for index, value := range values {
		elements[index] = arrayElement{text: value}
	}
	return formatArray(elements)
}

func cut(arguments []driver.Value, fromLeft bool) driver.Value {
	count, ok := integerValue(arguments[1])
	if arguments[0] == nil || !ok {
		return nil
	}
	runes := []rune(textValue(arguments[0]))
	length := int64(len(runes))
	if count < 0 {
		count = max(length+count, 0)
	}
	count = min(count, length)
	if fromLeft {
		return string(runes[:count])
	}
	return string(runes[length-count:])
}

func pad(arguments []driver.Value, left bool) driver.Value {
	if len(arguments) < 2 || arguments[0] == nil {
		return nil
	}
	length, ok := integerValue(arguments[1])
	if !ok {
		return nil
	}
	length = min(max(length, 0), 1<<20)
	filler := " "
	if len(arguments) > 2 {
		filler = textValue(arguments[2])
	}
	runes := []rune(textValue(arguments[0]))
	if int64(len(runes)) >= length {
		return string(runes[:length])
	}
	fill := []rune(filler)
	if len(fill) == 0 {
		return string(runes)
	}
	padding := make([]rune, 0, length-int64(len(runes)))
	for int64(len(padding)) < length-int64(len(runes)) {
		padding = append(padding, fill[len(padding)%len(fill)])
	}
	if left {
		return string(padding) + string(runes)
	}
	return string(runes) + string(padding)
}

// arrayAggregate is array_agg: the values of a group as an array in PostgreSQL's text form.
type arrayAggregate struct {
	elements []arrayElement
}

func (aggregate *arrayAggregate) Step(_ *sqlite.FunctionContext, arguments []driver.Value) error {
	value := arguments[0]
	_, number := value.(int64)
	if _, real := value.(float64); real {
		number = true
	}
	aggregate.elements = append(aggregate.elements, arrayElement{text: textValue(value), null: value == nil, number: number})
	return nil
}

func (aggregate *arrayAggregate) WindowInverse(*sqlite.FunctionContext, []driver.Value) error {
	if len(aggregate.elements) > 0 {
		aggregate.elements = aggregate.elements[1:]
	}
	return nil
}

func (aggregate *arrayAggregate) WindowValue(*sqlite.FunctionContext) (driver.Value, error) {
	if len(aggregate.elements) == 0 {
		return nil, nil
	}
	return formatArray(aggregate.elements), nil
}

func (aggregate *arrayAggregate) Final(*sqlite.FunctionContext) {}
