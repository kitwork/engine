package work

import "github.com/kitwork/engine/value"

type Kitwork struct {
	Debug bool
}

func NewKitwork() *Kitwork {
	return &Kitwork{}
}

func (k *Kitwork) Router(...value.Value) *Router {
	return &Router{}
}

type Get struct {
}

type Post struct {
}

type Put struct {
}

type Delete struct {
}
