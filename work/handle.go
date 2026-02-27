package work

import (
	"github.com/kitwork/engine/value"
)

type Handle struct {
	guard *value.Lambda
	main  *value.Lambda
	done  *value.Lambda
	fail  *value.Lambda
}

func (h *Handle) Guard(cb value.Value) *Handle {
	if sFn, ok := cb.V.(*value.Lambda); ok {
		h.guard = sFn
	}
	return h
}

func (h *Handle) Main(cb value.Value) *Handle {
	if sFn, ok := cb.V.(*value.Lambda); ok {
		h.main = sFn
	}
	return h
}

func (h *Handle) Done(cb value.Value) *Handle {
	if sFn, ok := cb.V.(*value.Lambda); ok {
		h.done = sFn
	}
	return h
}

func (h *Handle) Fail(cb value.Value) *Handle {
	if sFn, ok := cb.V.(*value.Lambda); ok {
		h.fail = sFn
	}
	return h
}
