package core

import (
	"github.com/kitwork/engine/value"
)

type Result struct {
	Value    value.Value
	Response value.Value
	ResType  string
	Error    string
	Energy   uint64
}

type GlobalConfig struct {
	Port    int
	Sources []string
	Debug   bool
}
