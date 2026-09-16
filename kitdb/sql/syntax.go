package sql

import (
	"fmt"
	"strings"
)

// StatementKind identifies the top-level syntax family before schema binding.
type StatementKind uint8

const (
	StatementUnknown StatementKind = iota
	StatementExplain
	StatementSelect
	StatementInsert
	StatementUpdate
	StatementDelete
	StatementCreate
	StatementAlter
	StatementDrop
	StatementAnalyze
	StatementReindex
	StatementPragma
	StatementSavepoint
)

func (kind StatementKind) String() string {
	switch kind {
	case StatementExplain:
		return "explain"
	case StatementSelect:
		return "select"
	case StatementInsert:
		return "insert"
	case StatementUpdate:
		return "update"
	case StatementDelete:
		return "delete"
	case StatementCreate:
		return "create"
	case StatementAlter:
		return "alter"
	case StatementDrop:
		return "drop"
	case StatementAnalyze:
		return "analyze"
	case StatementReindex:
		return "reindex"
	case StatementPragma:
		return "pragma"
	case StatementSavepoint:
		return "savepoint"
	default:
		return "unknown"
	}
}

// StatementEnvelope is the syntax-only result shared by all KitDB frontends.
// Body is the token index immediately after the top-level SELECT/WITH verb;
// for EXPLAIN it follows the required SELECT or WITH. ExplainAnalyze records
// the execution-bearing EXPLAIN form without changing the query grammar.
type StatementEnvelope struct {
	Kind           StatementKind
	Tokens         []Token
	Body           int
	ExplainAnalyze bool
	With           bool
}

// ParseEnvelope lexes and classifies one supported KitDB statement without
// parsing expressions or binding catalog names.
func ParseEnvelope(source string) (StatementEnvelope, error) {
	tokens, err := Lex(strings.TrimSpace(source))
	if err != nil {
		return StatementEnvelope{}, err
	}
	cursor, err := NewCursor(tokens, 0)
	if err != nil {
		return StatementEnvelope{}, err
	}
	first := cursor.Take()
	if first.Kind != TokenIdentifier {
		return StatementEnvelope{}, unsupportedStatementError()
	}

	kind := StatementUnknown
	explainAnalyze := false
	with := false
	switch strings.ToLower(first.Text) {
	case "explain":
		kind = StatementExplain
		queryPlan := cursor.AcceptKeyword("query")
		if queryPlan {
			if err := cursor.ExpectKeyword("plan"); err != nil {
				return StatementEnvelope{}, err
			}
		}
		explainAnalyze = cursor.AcceptKeyword("analyze")
		if queryPlan && explainAnalyze {
			return StatementEnvelope{}, fmt.Errorf("kitdb SQL: EXPLAIN QUERY PLAN cannot be combined with ANALYZE")
		}
		if !cursor.AcceptKeyword("select") {
			if !cursor.AcceptKeyword("with") {
				return StatementEnvelope{}, fmt.Errorf("kitdb SQL: EXPLAIN accepts SELECT or WITH only")
			}
			with = true
		}
	case "select":
		kind = StatementSelect
	case "with":
		kind = StatementSelect
		with = true
	case "insert":
		kind = StatementInsert
	case "update":
		kind = StatementUpdate
	case "delete":
		kind = StatementDelete
	case "create":
		kind = StatementCreate
	case "alter":
		kind = StatementAlter
	case "drop":
		kind = StatementDrop
	case "analyze":
		kind = StatementAnalyze
	case "reindex":
		kind = StatementReindex
	case "pragma":
		kind = StatementPragma
	case "savepoint", "release":
		kind = StatementSavepoint
	case "rollback":
		// Full ROLLBACK remains connection-owned; classify only ROLLBACK TO.
		at := cursor.Position()
		if at < len(tokens) && (strings.EqualFold(tokens[at].Text, "work") || strings.EqualFold(tokens[at].Text, "transaction")) {
			at++
		}
		if at >= len(tokens) || !strings.EqualFold(tokens[at].Text, "to") {
			return StatementEnvelope{}, unsupportedStatementError()
		}
		kind = StatementSavepoint
	default:
		return StatementEnvelope{}, unsupportedStatementError()
	}
	return StatementEnvelope{
		Kind: kind, Tokens: tokens, Body: cursor.Position(), ExplainAnalyze: explainAnalyze, With: with,
	}, nil
}

func unsupportedStatementError() error {
	return fmt.Errorf("kitdb SQL: only SELECT/WITH, EXPLAIN [QUERY PLAN | ANALYZE] SELECT/WITH, INSERT, UPDATE, DELETE, ANALYZE, REINDEX, supported CREATE/ALTER/DROP and PRAGMA statements are accepted")
}
