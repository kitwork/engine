package sql

import (
	"errors"
	"strings"
)

type LocalRoleStatement struct {
	Action, Role, Table, Database, User string
	Revoke                              bool
}

// This deliberately distinguishes database-local roles from root login users.
func ParseLocalRoleStatement(source string) (*LocalRoleStatement, bool, error) {
	if !ContainsFold(source, "role") && !ContainsFold(source, "grant") && !ContainsFold(source, "revoke") {
		return nil, false, nil
	}
	tokens, err := Lex(source)
	if err != nil {
		return nil, false, nil
	}
	c, err := NewCursor(tokens, 0)
	if err != nil {
		return nil, false, nil
	}
	first := c.Take()
	if first.Kind != TokenIdentifier {
		return nil, false, nil
	}
	action := strings.ToLower(first.Text)
	r := &LocalRoleStatement{Revoke: action == "revoke"}
	invalid := errors.New("invalid local role statement")
	name := func() (string, bool) { t := c.Take(); return strings.ToLower(t.Text), t.Kind == TokenIdentifier }
	var ok bool
	if action == "create" || action == "drop" {
		if !c.AcceptKeyword("role") {
			return nil, false, nil
		}
		r.Action = action
		r.Role, ok = name()
		if !ok {
			return nil, true, invalid
		}
	} else if action == "grant" || action == "revoke" {
		if c.AcceptKeyword("role") {
			r.Action = "map"
			r.Role, ok = name()
			if !ok || !c.AcceptKeyword("on") || !c.AcceptKeyword("database") {
				return nil, true, invalid
			}
			r.Database, ok = name()
			if !ok {
				return nil, true, invalid
			}
		} else if privilege := strings.ToLower(c.Peek().Text); privilege == "select" || privilege == "insert" || privilege == "update" || privilege == "delete" {
			c.Take()
			r.Action = privilege
			if !c.AcceptKeyword("on") || !c.AcceptKeyword("table") {
				return nil, true, invalid
			}
			r.Table, ok = name()
			if !ok {
				return nil, true, invalid
			}
		} else {
			return nil, false, nil
		}
		direction := "to"
		if r.Revoke {
			direction = "from"
		}
		if !c.AcceptKeyword(direction) {
			return nil, true, invalid
		}
		if r.Action != "map" {
			if !c.AcceptKeyword("role") {
				return nil, true, invalid
			}
			r.Role, ok = name()
		} else {
			r.User, ok = name()
		}
		if !ok {
			return nil, true, invalid
		}
	} else {
		return nil, false, nil
	}
	c.AcceptSymbol(";")
	if c.Peek().Kind != TokenEOF {
		return nil, true, invalid
	}
	return r, true, nil
}
