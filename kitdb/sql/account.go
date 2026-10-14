package sql

import (
	"errors"
	"strconv"
	"strings"
)

// AccountStatement is separate from data/schema statements: it mutates the
// managed root owner, never the selected database transaction.
type AccountStatement struct {
	Action, User, Database, Password string
	PasswordParameter                int
	Permissions                      []string
	PasswordSpecified                bool
	Disabled                         *bool
	IfExists                         bool
}

func ContainsFold(source, keyword string) bool {
	for i := 0; i+len(keyword) <= len(source); i++ {
		if strings.EqualFold(source[i:i+len(keyword)], keyword) {
			return true
		}
	}
	return false
}

func AccountCandidate(source string) bool {
	return ContainsFold(source, "user") || ContainsFold(source, "grant") || ContainsFold(source, "revoke")
}

// ParseAccountStatement returns generic errors so a malformed password clause
// cannot echo credentials through cursor diagnostics.
func ParseAccountStatement(source string) (*AccountStatement, bool, error) {
	if !AccountCandidate(source) {
		return nil, false, nil
	}
	invalid := errors.New("invalid account management statement")
	tokens, err := Lex(source)
	if err != nil {
		return nil, true, invalid
	}
	cursor, err := NewCursor(tokens, 0)
	if err != nil {
		return nil, true, invalid
	}
	first := cursor.Take()
	if first.Kind != TokenIdentifier {
		return nil, false, nil
	}
	action := strings.ToLower(first.Text)
	if action == "create" || action == "alter" || action == "drop" {
		if !cursor.AcceptKeyword("user") {
			return nil, false, nil
		}
	} else if action != "grant" && action != "revoke" {
		return nil, false, nil
	}
	result := &AccountStatement{Action: action}
	identifier := func() (string, bool) { token := cursor.Take(); return token.Text, token.Kind == TokenIdentifier }
	passwordValue := func() bool {
		token := cursor.Take()
		result.PasswordSpecified = true
		switch token.Kind {
		case TokenString:
			result.Password = token.Text
			return true
		case TokenPlaceholder:
			if !strings.HasPrefix(token.Text, "$") {
				return false
			}
			result.PasswordParameter, err = strconv.Atoi(token.Text[1:])
			return err == nil && result.PasswordParameter > 0
		}
		return false
	}
	if action == "drop" {
		if cursor.AcceptKeyword("if") {
			if !cursor.AcceptKeyword("exists") {
				return nil, true, invalid
			}
			result.IfExists = true
		}
		var ok bool
		result.User, ok = identifier()
		if !ok {
			return nil, true, invalid
		}
	} else if action == "alter" {
		var ok bool
		result.User, ok = identifier()
		if !ok {
			return nil, true, invalid
		}
		cursor.AcceptKeyword("with")
		for cursor.Peek().Kind != TokenEOF && !(cursor.Peek().Kind == TokenSymbol && cursor.Peek().Text == ";") {
			switch {
			case cursor.AcceptKeyword("password"):
				if result.PasswordSpecified || !passwordValue() {
					return nil, true, invalid
				}
			case cursor.AcceptKeyword("login"):
				if result.Disabled != nil {
					return nil, true, invalid
				}
				value := false
				result.Disabled = &value
			case cursor.AcceptKeyword("nologin"):
				if result.Disabled != nil {
					return nil, true, invalid
				}
				value := true
				result.Disabled = &value
			default:
				return nil, true, invalid
			}
		}
		if !result.PasswordSpecified && result.Disabled == nil {
			return nil, true, invalid
		}
	} else if action == "create" {
		var ok bool
		result.User, ok = identifier()
		if !ok || !cursor.AcceptKeyword("with") || !cursor.AcceptKeyword("password") {
			return nil, true, invalid
		}
		if !passwordValue() {
			return nil, true, invalid
		}
	} else {
		seen := map[string]bool{}
		for {
			permission, ok := identifier()
			permission = strings.ToLower(permission)
			if !ok || seen[permission] || (permission != "connect" && permission != "read" && permission != "write" && permission != "schema") {
				return nil, true, invalid
			}
			seen[permission] = true
			result.Permissions = append(result.Permissions, permission)
			if !cursor.AcceptSymbol(",") {
				break
			}
		}
		if !cursor.AcceptKeyword("on") || !cursor.AcceptKeyword("database") {
			return nil, true, invalid
		}
		var ok bool
		result.Database, ok = identifier()
		if !ok {
			return nil, true, invalid
		}
		direction := "to"
		if action == "revoke" {
			direction = "from"
		}
		if !cursor.AcceptKeyword(direction) {
			return nil, true, invalid
		}
		result.User, ok = identifier()
		if !ok {
			return nil, true, invalid
		}
	}
	cursor.AcceptSymbol(";")
	if cursor.Peek().Kind != TokenEOF {
		return nil, true, invalid
	}
	return result, true, nil
}

// AccountTraceSource is conservative on malformed SQL as well as valid commands.
func AccountTraceSource(source string) string {
	if ContainsFold(source, "password") {
		return "[redacted account/password SQL]"
	}
	return source
}
