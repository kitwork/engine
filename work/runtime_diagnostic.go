package work

import (
	"errors"
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
	if !router.failed {
		// the first failure is the one a .error() handler describes (errorObject)
		router.failure, router.failed = result, true
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
			// the whole text, stack included: in production the visitor reads only the request id
			"diagnostic", result.Text(),
		)
		if diagnostic.CauseCode != "" {
			attributes = append(attributes, "cause_code", diagnostic.CauseCode)
		}
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

// recordFailure logs a request's failure that is not a result of the VM (recordRuntimeFailure logs
// those): a page that does not compile, a generated output or a proxy that failed, a value the engine
// could not write. The record keeps the error whole; in production the visitor reads only the
// request id.
func (t *Tenant) recordFailure(router *Router, stage string, err error) {
	if t == nil || router == nil || router.request == nil || err == nil {
		return
	}
	generation := uint64(0)
	if current := t.SiteGeneration(); current != nil {
		generation = current.Version()
	}
	slog.ErrorContext(
		router.request.Context(),
		"Kitwork request failed",
		"request_id", router.requestID,
		"app", t.AppID(),
		"site", t.Domain(),
		"generation", generation,
		"method", router.request.Method,
		"path", router.request.URL.Path,
		"stage", stage,
		"message", err.Error(),
	)
}

// stageFailure is the error of a stage other than the handler (a proxy, a generated output) whose run
// of the program failed (run), or whose upstream gave no answer. Its text is the whole of it, stack and
// URL included, for the log record and the answer while developing; failure is what a .error()
// handler's error describes (errorObject): the run's own message, code and place, or the upstream's
// failure with the message a program reads of a request (no query string, no stack).
type stageFailure struct {
	text    string
	failure value.Value
	run     bool
}

func (e *stageFailure) Error() string { return e.text }

// recordStageFailure logs the failure of a stage and keeps what a .error() handler describes of it:
// a failed run as recordRuntimeFailure does, any other failure as recordFailure does.
func (t *Tenant) recordStageFailure(router *Router, bytecode *compiler.Bytecode, stage string, err error) {
	var failed *stageFailure
	if errors.As(err, &failed) {
		if failed.run {
			t.recordRuntimeFailure(router, bytecode, stage, failed.failure)
			return
		}
		if router != nil && !router.failed {
			router.failure, router.failed = failed.failure, true
		}
	}
	t.recordFailure(router, stage, err)
}
