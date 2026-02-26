package work

import (
	"github.com/kitwork/engine/value"
)

type Handle struct {
	guard *value.Script
	main  *value.Script
	done  *value.Script
	fail  *value.Script
}

func (h *Handle) Guard(cb value.Value) *Handle {
	if sFn, ok := cb.V.(*value.Script); ok {
		h.guard = sFn
	}
	return h
}

func (h *Handle) Main(cb value.Value) *Handle {
	if sFn, ok := cb.V.(*value.Script); ok {
		h.main = sFn
	}
	return h
}

func (h *Handle) Done(cb value.Value) *Handle {
	if sFn, ok := cb.V.(*value.Script); ok {
		h.done = sFn
	}
	return h
}

func (h *Handle) Fail(cb value.Value) *Handle {
	if sFn, ok := cb.V.(*value.Script); ok {
		h.fail = sFn
	}
	return h
}
