package work

// What a handler may take apart in its first parameter (19/10/2026): the objects of its context, never
// a function of it. A function taken out of an object loses its `this` in JavaScript, so
// ({ json }) => json(data) works in no JavaScript; the file stays one that runs the same as plain
// JavaScript by refusing it where it is written. A route takes request and response; a job or a
// schedule takes job.

import (
	"fmt"
	"path"
	"reflect"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// routeDestructurable is the names a route handler's first parameter may take apart (a job's and a
// schedule's are queueDestructurable and scheduleDestructurable).
var routeDestructurable = []string{"request", "response"}

// hasMethod reports whether a host type answers name, as a member read of it in a program does
// (json → JSON, notFound → NotFound).
func hasMethod(host any, name string) bool {
	kind := reflect.TypeOf(host)
	for i := 0; i < kind.NumMethod(); i++ {
		if strings.EqualFold(kind.Method(i).Name, name) {
			return true
		}
	}
	return false
}

// destructureError is the error of a handler whose first parameter takes apart what it may not, nil
// when it takes nothing apart or only names of allowed. side says what the handler's context offers a
// name it may not take (routeSide, queueSide, scheduleSide).
func destructureError(lambda *value.Lambda, allowed []string, side func(name string) string) error {
	if lambda == nil || len(lambda.Params) == 0 {
		return nil
	}
	entries := compiler.DestructuredEntries(lambda.Params[0])
	if entries == nil {
		return nil
	}
	list := "tên tách được: " + strings.Join(allowed, ", ")
	for _, entry := range entries {
		isAllowed := false
		for _, name := range allowed {
			isAllowed = isAllowed || entry.Key == name
		}
		var why string
		switch {
		case !isAllowed:
			why = side(entry.Key)
		case entry.Nested:
			inner := compiler.DestructuredNames(entry.Target)
			first := entry.Key
			if len(inner) > 0 {
				first = inner[0]
			}
			why = fmt.Sprintf("`%s` là hàm của `%s`; tách ra thì trong JavaScript nó mất `this`: tách `%s` rồi viết `%s.%s(…)`",
				first, entry.Key, entry.Key, entry.Key, first)
		default:
			continue
		}
		return fmt.Errorf("%s: %s; %s", lambdaPlace(lambda), why, list)
	}
	return nil
}

// routeSide says what a route's context offers name: a function of the response or of the request,
// one of the context's own, or nothing.
func routeSide(name string) string {
	switch {
	case hasMethod(&Response{}, name):
		return fmt.Sprintf("`%s` là hàm của `context`; tách ra thì trong JavaScript nó mất `this`: viết `context.%s(…)`, hoặc tách `response` rồi `response.%s(…)`", name, name, name)
	case hasMethod(&Request{}, name):
		return fmt.Sprintf("`%s` là hàm của `context`; tách ra thì trong JavaScript nó mất `this`: viết `context.%s(…)`, hoặc tách `request` rồi `request.%s(…)`", name, name, name)
	case hasMethod(&Context{}, name):
		return fmt.Sprintf("`%s` là hàm của `context`; tách ra thì trong JavaScript nó mất `this`: viết `context.%s(…)`", name, name)
	}
	return fmt.Sprintf("`%s` không có trong `context` của một route", name)
}

// lambdaPlace is where a function is written, file:line:column, as far as the program knows.
func lambdaPlace(lambda *value.Lambda) string {
	file := path.Base(strings.ReplaceAll(lambda.SourceFile, "\\", "/"))
	if lambda.SourceFile == "" {
		file = "?"
	}
	if lambda.SourceLine <= 0 {
		return file
	}
	return fmt.Sprintf("%s:%d:%d", file, lambda.SourceLine, lambda.SourceColumn)
}

// removedParameterError is the error of a route's function that takes sse, the helper a handler was handed
// after its context until 22/10/2026 ((ctx, sse) => sse.connect(…)): the stream is context.sse now, and
// the old helper is gone rather than handed as nothing.
func removedParameterError(lambda *value.Lambda) error {
	if lambda == nil {
		return nil
	}
	for _, name := range lambda.Params {
		if strings.EqualFold(name, "sse") {
			return fmt.Errorf("%s: %s", lambdaPlace(lambda), compiler.SSEParameterRemoved)
		}
	}
	return nil
}

// handlerDestructureError checks every function the folder's router file gave the router — each
// method's handler, guards and hooks, the folder's guards and its notFound/error — and is the first
// error by place in the file.
func (f *FolderRouter) handlerDestructureError() error {
	var lambdas []*value.Lambda
	add := func(ls ...*value.Lambda) {
		for _, l := range ls {
			if l != nil {
				lambdas = append(lambdas, l)
			}
		}
	}
	for _, m := range f.methods {
		add(m.handle, m.success, m.errorH, m.final)
		add(m.guards...)
	}
	for _, m := range f.outputs {
		add(m.handle, m.success, m.errorH, m.final)
		add(m.guards...)
	}
	add(f.guards...)
	add(f.notFound, f.errorH)
	var first error
	var at *value.Lambda
	for _, l := range lambdas {
		err := removedParameterError(l)
		if err == nil {
			err = destructureError(l, routeDestructurable, routeSide)
		}
		if err == nil {
			continue
		}
		if at == nil || l.SourceLine < at.SourceLine || (l.SourceLine == at.SourceLine && l.SourceColumn < at.SourceColumn) {
			first, at = err, l
		}
	}
	return first
}
