package work

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

var requestSequence atomic.Uint64

func requestID(request *http.Request) string {
	if request != nil {
		candidate := strings.TrimSpace(request.Header.Get("X-Request-ID"))
		if candidate != "" &&
			len(candidate) <= 128 &&
			!strings.ContainsAny(candidate, "\r\n") {
			return candidate
		}
	}
	return fmt.Sprintf(
		"kw-%x-%x",
		time.Now().UnixNano(),
		requestSequence.Add(1),
	)
}

func (t *Tenant) recordRuntimeFailure(
	router *Router,
	bytecode *compiler.Bytecode,
	stage string,
	result value.Value,
) {
	if router == nil || result.K != value.Invalid {
		return
	}

	program := ""
	if bytecode != nil && bytecode.Program != nil {
		program = bytecode.Program.Checksum()
	}
	generation := uint64(0)
	if current := t.SiteGeneration(); current != nil {
		generation = current.Version()
	}

	attributes := []any{
		"request_id", router.requestID,
		"app", t.AppID(),
		"site", t.Domain(),
		"generation", generation,
		"method", router.request.Method,
		"path", router.request.URL.Path,
		"stage", stage,
		"program", program,
	}
	if diagnostic, ok := runtime.DiagnosticFrom(result); ok {
		attributes = append(
			attributes,
			"code", diagnostic.Code,
			"message", diagnostic.Message,
			"function", diagnostic.Function,
			"source", diagnostic.File,
			"line", diagnostic.Line,
			"column", diagnostic.Column,
			"byte", diagnostic.IP,
			"stack_depth", len(diagnostic.Stack),
			"suppressed", len(diagnostic.Suppressed),
		)
	} else {
		attributes = append(
			attributes,
			"code", runtime.DiagnosticRuntimeError,
			"message", result.Text(),
		)
	}
	slog.ErrorContext(
		router.request.Context(),
		"Kitwork runtime execution failed",
		attributes...,
	)
}
