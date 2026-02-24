package core

import (
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/work"
)

type Result struct {
	Value    value.Value
	Response work.Response
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
