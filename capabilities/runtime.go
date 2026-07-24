package capabilities

import (
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// Runtime is the COMPUTE seam a capability obtains by asserting its Scope: scope.(Runtime).
//
// Scope gives a capability DATA (AppID/Domain/ResolvePath/DB); Runtime adds EXECUTION — running user
// JS OFF the request path. A cron scheduler firing handlers on a timer needs this, as will future
// scheduled / webhook capabilities. Scope stays data-only so capabilities that never compute don't
// see it; the host (work.Tenant) implements both interfaces.
type Runtime interface {
	// Execute runs a lambda against its own bytecode in a pooled VM bound to this scope's
	// globals/builtins and capped by the tenant's energy limit. Returns gas consumed and any error
	// (a thrown value or an energy-limit halt).
	Execute(bc *compiler.Bytecode, fn *value.Lambda, args []value.Value) (gas uint64, err error)
}
