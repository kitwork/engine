package work

import "github.com/kitwork/engine/value"

type Kitwork struct {
	debug bool
}

func NewKitwork(config ...value.Value) *Kitwork {
	return &Kitwork{}
}
