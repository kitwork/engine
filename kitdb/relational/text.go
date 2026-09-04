package relational

import (
	"fmt"
	"strings"
	"unicode/utf8"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func coerceTextField(field kitdbsql.Field, item any, explicit bool) (string, error) {
	text, ok := item.(string)
	if !ok {
		return "", fmt.Errorf("field %q expects text, got %T", field.Name, item)
	}
	if field.TextLength == nil {
		return text, nil
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("field %q expects valid UTF-8 text", field.Name)
	}
	limit := *field.TextLength
	characters := utf8.RuneCountInString(text)
	if characters > limit {
		boundary := textRuneBoundary(text, limit)
		if !explicit && strings.Trim(text[boundary:], " ") != "" {
			return "", fmt.Errorf("field %q value exceeds %s(%d)", field.Name, strings.ToUpper(field.Kind), limit)
		}
		text, characters = text[:boundary], limit
	}
	if field.Kind == "char" && characters < limit {
		text += strings.Repeat(" ", limit-characters)
	}
	return text, nil
}

func textRuneBoundary(text string, characters int) int {
	if characters <= 0 {
		return 0
	}
	seen := 0
	for offset := range text {
		if seen == characters {
			return offset
		}
		seen++
	}
	return len(text)
}

func exactCharacterField(field kitdbsql.Field) bool {
	return field.Kind == "char" && field.TextLength != nil
}

func exactCharacterPredicate(expression *boundPredicate) bool {
	if expression == nil {
		return false
	}
	if expression.field != nil {
		return exactCharacterField(*expression.field)
	}
	return expression.kind == "cast" && expression.castKind == "char" && expression.textLength != nil
}

func characterText(item any) (string, bool) {
	switch current := item.(type) {
	case string:
		return current, true
	default:
		return "", false
	}
}

func expressionText(expression *boundPredicate, item any) (string, bool) {
	text, ok := characterText(item)
	if ok && exactCharacterPredicate(expression) {
		text = strings.TrimRight(text, " ")
	}
	return text, ok
}

func foreignComparableValue(source, target kitdbsql.Field, item any) (any, error) {
	if item == nil || !exactCharacterField(source) && !exactCharacterField(target) {
		return item, nil
	}
	if exactCharacterField(source) {
		text, ok := characterText(item)
		if !ok {
			return nil, fmt.Errorf("field %q expects text, got %T", source.Name, item)
		}
		item = strings.TrimRight(text, " ")
	}
	return coerceField(target, item)
}

func compareCharacterField(_ kitdbsql.Field, left, right any) (int, bool) {
	leftText, leftOK := characterText(left)
	rightText, rightOK := characterText(right)
	if !leftOK || !rightOK {
		return 0, false
	}
	return strings.Compare(strings.TrimRight(leftText, " "), strings.TrimRight(rightText, " ")), true
}
