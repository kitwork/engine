package render

import (
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

// expressionSource writes a compiled template expression back as the template wrote it, for a message
// that names a call: item.price, src.highlight(…). A part it cannot write back is (…).
func expressionSource(compiled *expression) string {
	if compiled == nil {
		return "(…)"
	}
	switch compiled.kind {
	case expressionPath:
		return strings.Join(compiled.parts, ".")
	case expressionLiteral:
		if compiled.value.K == value.String {
			return strconv.Quote(compiled.value.Text())
		}
		return compiled.value.Text()
	case expressionMethod:
		call := "()"
		if len(compiled.args) > 0 {
			call = "(…)"
		}
		return expressionSource(compiled.left) + "." + compiled.parts[0] + call
	}
	return "(…)"
}
