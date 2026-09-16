package relational

import (
	"errors"
	"fmt"
	"strings"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const maximumSQLSavepoints = 64

var errSavepointMissing = errors.New("kitdb SQL: savepoint does not exist")
var errSavepointTransaction = errors.New("kitdb SQL: savepoint requires an explicit data transaction")

type namedSavepoint struct {
	name  string
	point Savepoint
}

func isSavepointSQL(source string) bool {
	trimmed := strings.TrimSpace(source)
	end := 0
	for end < len(trimmed) && (trimmed[end] >= 'a' && trimmed[end] <= 'z' || trimmed[end] >= 'A' && trimmed[end] <= 'Z') {
		end++
	}
	if end > 0 {
		switch strings.ToLower(trimmed[:end]) {
		case "savepoint", "release", "rollback":
		default:
			return false
		}
	}
	// Comments and quoted tokens still go through the shared lexer; ordinary
	// SELECT/INSERT calls do not pay for a second lexical pass here.
	envelope, err := kitdbsql.ParseEnvelope(source)
	return err == nil && envelope.Kind == kitdbsql.StatementSavepoint
}

func (transaction *Transaction) executeSavepoint(plan *kitdbsql.SavepointStatement) (Result, error) {
	if plan == nil || plan.Name == "" || len(plan.Name) > 128 {
		return Result{}, fmt.Errorf("kitdb SQL: invalid savepoint plan")
	}
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	if transaction.done {
		return Result{}, fmt.Errorf("kitdb: relational transaction is closed")
	}
	switch plan.Action {
	case "savepoint":
		if len(transaction.savepoints) >= maximumSQLSavepoints {
			return Result{}, fmt.Errorf("kitdb SQL: transaction exceeds %d savepoints", maximumSQLSavepoints)
		}
		transaction.savepoints = append(transaction.savepoints, namedSavepoint{plan.Name, Savepoint{transaction, len(transaction.operations), transaction.bytes}})
	case "rollback", "release":
		index := len(transaction.savepoints) - 1
		for index >= 0 && transaction.savepoints[index].name != plan.Name {
			index--
		}
		if index < 0 {
			return Result{}, fmt.Errorf("%w: %s", errSavepointMissing, plan.Name)
		}
		if plan.Action == "rollback" {
			point := transaction.savepoints[index].point
			if err := transaction.rollbackToLocked(point); err != nil {
				return Result{}, err
			}
			transaction.savepoints = transaction.savepoints[:index+1]
		} else {
			transaction.savepoints = transaction.savepoints[:index]
		}
	default:
		return Result{}, fmt.Errorf("kitdb SQL: invalid savepoint action")
	}
	return Result{CommandTag: strings.ToUpper(plan.Action)}, nil
}
