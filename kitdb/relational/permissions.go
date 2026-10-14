package relational

import (
	"strings"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

// Nil is reserved for host-trusted/legacy adapters. Managed clients always
// carry an immutable policy snapshot, including explicit connect-only grants.
func checkDatabaseStatement(policy *managed.DatabasePermissions, source string) error {
	if isAccountCatalogQuery(source) {
		return accountCatalogReadOnly(source)
	}
	if policy == nil {
		return nil
	}
	if policy.Read && policy.Write && policy.Schema {
		return nil
	}
	normalized := normalizePostgresSQL(source)
	switch normalized {
	case "select 1", "discard all", "commit", "commit transaction", "commit work",
		"end", "end transaction", "end work", "rollback", "rollback transaction", "rollback work":
		return nil
	}
	if begin, _, err := parsePostgresBegin(normalized); begin {
		return err
	}
	if strings.HasPrefix(normalized, "set ") || strings.HasPrefix(normalized, "show ") {
		return nil
	}
	if isPostgresCatalogQuery(source) {
		envelope, err := kitdbsql.ParseEnvelope(source)
		if err != nil {
			return err
		}
		if envelope.Kind == kitdbsql.StatementSelect || envelope.Kind == kitdbsql.StatementExplain {
			if !policy.Read {
				return pgwire.NewError("42501", "permission denied for database metadata")
			}
			for _, token := range envelope.Tokens {
				if !policy.Write && token.Kind == kitdbsql.TokenIdentifier &&
					(strings.EqualFold(token.Text, "nextval") || strings.EqualFold(token.Text, "setval")) {
					return pgwire.NewError("42501", "permission denied for sequence mutation")
				}
			}
			return nil
		}
	}
	statement, err := kitdbsql.ParseStatement(source)
	if err != nil {
		return err
	}
	allowed := false
	switch statement.Kind {
	case kitdbsql.StatementSelect, kitdbsql.StatementExplain, kitdbsql.StatementPragma:
		allowed = policy.Read
		if statement.Select != nil && selectContainsSequenceFunction(statement.Select) && !policy.Write {
			// Sequence mutations are supported only as direct scalar projections.
			for _, projection := range statement.Select.Projection {
				if expressionMutatesSequence(projection.Expression) {
					allowed = false
				}
			}
		}
	case kitdbsql.StatementInsert, kitdbsql.StatementUpdate, kitdbsql.StatementDelete:
		allowed = policy.Write
		if !policy.Read {
			if statement.Insert != nil && (len(statement.Insert.Returning) != 0 || statement.Insert.Select != nil) ||
				statement.Update != nil && len(statement.Update.Returning) != 0 ||
				statement.Delete != nil && len(statement.Delete.Returning) != 0 {
				allowed = false
			}
		}
	case kitdbsql.StatementCreate, kitdbsql.StatementAlter, kitdbsql.StatementDrop,
		kitdbsql.StatementAnalyze, kitdbsql.StatementReindex:
		allowed = policy.Schema
	case kitdbsql.StatementSavepoint:
		allowed = true
	}
	if !allowed {
		return pgwire.NewError("42501", "permission denied for database operation")
	}
	return nil
}

func expressionMutatesSequence(node *kitdbsql.ExpressionPlan) bool {
	if node == nil {
		return false
	}
	if node.Kind == "function" && (node.Operator == "nextval" || node.Operator == "setval") {
		return true
	}
	for i := range node.Arguments {
		if expressionMutatesSequence(&node.Arguments[i]) {
			return true
		}
	}
	return false
}
