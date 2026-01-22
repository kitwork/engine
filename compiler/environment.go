package compiler

import (
	"github.com/kitwork/engine/value"
)

type Environment struct {
	store map[string]value.Value
	outer *Environment
}

func NewEnvironment() *Environment {
	return &Environment{store: make(map[string]value.Value)}
}

func (e *Environment) SetOuter(outer *Environment) {
	e.outer = outer
}

func (e *Environment) Reset() {
	// Xóa tất cả các khóa trong map nhưng giữ lại vùng nhớ (capacity)
	// Cách này trong Go giúp tái sử dụng vùng nhớ cực hiệu quả
	for k := range e.store {
		delete(e.store, k)
	}
	e.outer = nil
}

// Tạo Scope con (dùng cho hàm hoặc block {})
func NewEnclosedEnvironment(outer *Environment) *Environment {
	env := NewEnvironment()
	env.outer = outer
	return env
}

func (e *Environment) Get(name string) (value.Value, bool) {
	val, ok := e.store[name]
	if !ok && e.outer != nil {
		return e.outer.Get(name)
	}
	return val, ok
}

func (e *Environment) Set(name string, val value.Value) value.Value {
	e.store[name] = val
	return val
}

// All trả về tất cả các biến có sẵn trong scope hiện tại (bao gồm cả scope cha)
func (e *Environment) All() map[string]value.Value {
	res := make(map[string]value.Value)
	if e.outer != nil {
		for k, v := range e.outer.All() {
			res[k] = v
		}
	}
	for k, v := range e.store {
		res[k] = v
	}
	return res
}
