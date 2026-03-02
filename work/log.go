package work

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

func (w *KitWork) Log() *Log { return &Log{tenant: w.tenant} }

type Log struct {
	tenant *Tenant
}

func (l *Log) Print(v value.Value) { fmt.Println(v) }
