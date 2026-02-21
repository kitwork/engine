package work

import (
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

type IWork interface {
	GetBase() *BaseWork
}

type BaseWork struct {
	Name        string
	TenantID    string
	Ver         string
	Description string
	Retries     int
	TimeoutDur  time.Duration
	Bytecode    *compiler.Bytecode
	SourcePath  string

	MainHandler *value.Script
	DoneHandler *value.Script
	FailHandler *value.Script
}

func (b *BaseWork) GetBase() *BaseWork { return b }

type WorkFluent[T any] struct {
	*BaseWork
	child T
}

func (w *WorkFluent[T]) Retry(times int, _ string) T { w.Retries = times; return w.child }
func (w *WorkFluent[T]) Desc(d string) T             { w.Description = d; return w.child }
func (w *WorkFluent[T]) Version(v string) T          { w.Ver = v; return w.child }
func (w *WorkFluent[T]) Done(fn value.Value) T {
	if sFn, ok := fn.V.(*value.Script); ok {
		w.DoneHandler = sFn
	}
	return w.child
}
func (w *WorkFluent[T]) Fail(fn value.Value) T {
	if sFn, ok := fn.V.(*value.Script); ok {
		w.FailHandler = sFn
	}
	return w.child
}
