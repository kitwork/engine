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

type Asset struct {
	Dir  string
	Path string
}

type GlobalConfig struct {
	Port    int
	Sources []string
	Assets  []Asset
	Debug   bool
}
