package engine

import "github.com/kitwork/engine/value"

// Result chứa kết quả thực thi và metadata
type Result struct {
	value  value.Value
	errors error
	energy uint64
}

func (r *Result) Value() any {
	if r.value.K == value.Nil {
		return nil
	}
	return r.value.Interface()
}

func (r *Result) Raw() value.Value {
	return r.value
}

func (r *Result) Error() error {
	return r.errors
}

func (r *Result) Energy() uint64 {
	return r.energy
}
