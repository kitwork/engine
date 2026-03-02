package work

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

type Log struct {
	tenant *Tenant
}

func (l *Log) Print(v value.Value) { fmt.Println(v) }

func (w *KitWork) Log() *Log { return &Log{tenant: w.tenant} }
