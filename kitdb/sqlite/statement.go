package sqlite

import (
	"fmt"
	"strings"
	"unicode"
)

type statementKind int

const (
	statementRead statementKind = iota
	statementData
	statementSchema
)

type statementWord struct {
	value string
	depth int
}

// statementShape is what the writer needs to know about one SQL text before it
// runs: its kind, and for a schema statement its leading words.
type statementShape struct {
	kind  statementKind
	words []string
}

// classifyStatement accepts exactly one statement. It refuses transaction
// control, which would commit or abandon a group transaction behind the
// writer's back, and any ROLLBACK conflict resolution, which would abandon it
// from inside a statement. Words are read outside string literals, quoted
// identifiers and comments.
func classifyStatement(text string) (statementShape, error) {
	var words []statementWord
	depth, after := 0, false
	runes := []rune(text)
	for position := 0; position < len(runes); {
		character := runes[position]
		switch {
		case unicode.IsSpace(character):
			position++
		case character == '-' && position+1 < len(runes) && runes[position+1] == '-':
			for position < len(runes) && runes[position] != '\n' {
				position++
			}
		case character == '/' && position+1 < len(runes) && runes[position+1] == '*':
			position += 2
			for position+1 < len(runes) && (runes[position] != '*' || runes[position+1] != '/') {
				position++
			}
			if position+1 >= len(runes) {
				return statementShape{}, fmt.Errorf("%w: unterminated comment", ErrStatementRefused)
			}
			position += 2
		case character == '\'' || character == '"' || character == '`' || character == '[':
			closing := character
			if character == '[' {
				closing = ']'
			}
			position++
			for {
				if position >= len(runes) {
					return statementShape{}, fmt.Errorf("%w: unterminated quote", ErrStatementRefused)
				}
				if runes[position] == closing {
					if closing != ']' && position+1 < len(runes) && runes[position+1] == closing {
						position += 2
						continue
					}
					position++
					break
				}
				position++
			}
			if after {
				return statementShape{}, fmt.Errorf("%w: one statement at a time", ErrStatementRefused)
			}
		case character == ';':
			if !after && createsTrigger(words) {
				// a trigger body holds semicolons; it is refused for what it is
				return statementShape{}, fmt.Errorf("%w: triggers are not recorded by the history", ErrUnsupportedSchema)
			}
			after = true
			position++
		case character == '_' || unicode.IsLetter(character) || unicode.IsDigit(character):
			start := position
			for position < len(runes) && (runes[position] == '_' || runes[position] == '$' ||
				unicode.IsLetter(runes[position]) || unicode.IsDigit(runes[position])) {
				position++
			}
			if after {
				return statementShape{}, fmt.Errorf("%w: one statement at a time", ErrStatementRefused)
			}
			words = append(words, statementWord{value: strings.ToUpper(string(runes[start:position])), depth: depth})
		default:
			if after {
				return statementShape{}, fmt.Errorf("%w: one statement at a time", ErrStatementRefused)
			}
			if character == '(' {
				depth++
			} else if character == ')' {
				depth--
			}
			position++
		}
	}
	if len(words) == 0 {
		return statementShape{}, fmt.Errorf("%w: empty statement", ErrStatementRefused)
	}
	for _, entry := range words {
		if entry.value == "ROLLBACK" {
			return statementShape{}, fmt.Errorf("%w: ROLLBACK would abandon the group transaction", ErrStatementRefused)
		}
	}
	values := make([]string, len(words))
	for position, entry := range words {
		values[position] = entry.value
	}
	switch values[0] {
	case "SELECT", "VALUES", "EXPLAIN":
		return statementShape{kind: statementRead, words: values}, nil
	case "INSERT", "UPDATE", "DELETE", "REPLACE":
		return statementShape{kind: statementData, words: values}, nil
	case "WITH":
		for _, entry := range words {
			if entry.depth == 0 {
				switch entry.value {
				case "INSERT", "UPDATE", "DELETE", "REPLACE":
					return statementShape{kind: statementData, words: values}, nil
				}
			}
		}
		return statementShape{kind: statementRead, words: values}, nil
	case "CREATE", "ALTER", "DROP", "ANALYZE", "REINDEX":
		for _, refused := range []string{"TRIGGER", "VIRTUAL", "TEMP", "TEMPORARY"} {
			if len(values) > 1 && values[1] == refused {
				return statementShape{}, fmt.Errorf("%w: CREATE %s is not recorded by the history", ErrUnsupportedSchema, refused)
			}
		}
		if values[0] == "DROP" && len(values) > 1 && values[1] == "TRIGGER" {
			return statementShape{}, fmt.Errorf("%w: triggers are not recorded by the history", ErrUnsupportedSchema)
		}
		return statementShape{kind: statementSchema, words: values}, nil
	}
	return statementShape{}, fmt.Errorf("%w: %s", ErrStatementRefused, values[0])
}

// createsTrigger reports CREATE [TEMP|TEMPORARY] TRIGGER.
func createsTrigger(words []statementWord) bool {
	if len(words) < 2 || words[0].value != "CREATE" {
		return false
	}
	if words[1].value == "TRIGGER" {
		return true
	}
	return len(words) > 2 && (words[1].value == "TEMP" || words[1].value == "TEMPORARY") && words[2].value == "TRIGGER"
}

// StatementKind says which call runs a statement.
type StatementKind int

const (
	// ReadStatement runs through Read: SELECT, VALUES, EXPLAIN, WITH … SELECT.
	ReadStatement StatementKind = iota
	// DataStatement runs through Write: INSERT, UPDATE, DELETE, REPLACE.
	DataStatement
	// SchemaStatement runs through Migrate: CREATE, ALTER, DROP, ANALYZE, REINDEX.
	SchemaStatement
)

// Classify applies the writer's statement rules to text: exactly one statement,
// no transaction control, nothing the history cannot record.
func Classify(text string) (StatementKind, error) {
	shape, err := classifyStatement(text)
	if err != nil {
		return 0, err
	}
	switch shape.kind {
	case statementData:
		return DataStatement, nil
	case statementSchema:
		return SchemaStatement, nil
	}
	return ReadStatement, nil
}
