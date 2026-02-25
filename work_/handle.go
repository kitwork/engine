package work

import (
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

func New(name string) *Work {
	return &Work{
		Name: name,
	}
}

// Work là Blueprint (Bản thiết kế) - IMMUTABLE
type Work struct {
	Name        string
	Entity      string // Multi-tenancy Isolation // base 36
	Domain      string // HTTP Domain / Host matching
	Version     string
	Description string
	Retries     int
	Timeout     time.Duration
	bytecode    *compiler.Bytecode
	SourcePath  string // Absolute path to the source file

	done   *value.Script
	fail   *value.Script
	handle *value.Script

	DoneFunc func(...value.Value) value.Value // Native Go handler

	TemplatePath string // Path to the template file
	ShellPath    string // Path to the explicit shell/template file
}

func (w *Work) Render(path string) *Work {
	w.TemplatePath = path
	return w
}

func (w *Work) Config(data map[string]any) *Work {
	return w
}

func (w *Work) Fail(fn value.Value) *Work {
	if sFn, ok := fn.V.(*value.Script); ok {
		w.fail = sFn
	}
	return w
}

func (w *Work) Done(fn value.Value) *Work {
	if sFn, ok := fn.V.(*value.Script); ok {
		w.done = sFn
	} else if fn.K == value.Func {
		if nativeFn, ok := fn.V.(func(...value.Value) value.Value); ok {
			w.DoneFunc = nativeFn
		}
	}
	return w
}

func (w *Work) GetDoneFunc() func(...value.Value) value.Value {
	return w.DoneFunc
}

func (w *Work) GetBytecode() *compiler.Bytecode {
	return w.bytecode
}

func (w *Work) SetBytecode(b *compiler.Bytecode) {
	w.bytecode = b
}

func (w *Work) GetFail() *value.Script {
	return w.fail
}

func (w *Work) GetDone() *value.Script {
	return w.done
}

func (w *Work) GetHandle() *value.Script {
	return w.handle
}
