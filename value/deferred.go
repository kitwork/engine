package value

// StatementFinalizer lets a value do deferred work when it is discarded as an expression statement —
// so a chain written flat in ANY order (http.get(url).then(A).catch(B).retry(3) OR .retry(3).then(A)…)
// fires exactly once at the end of the statement, no matter where the handlers sit. The VM calls
// FinalizeStatement on the value it is about to POP; when run is true it executes handler(arg) on the
// current VM (re-entrant, exactly like an array-map callback).
//
// The lazy http Request (helpers/http) implements this: ensure() runs the request + retry loop, then
// success returns the .then() lambda, final failure returns the .catch() lambda. A dangling request
// with no handler still fires (fire-and-forget) — FinalizeStatement returns run == false.
type StatementFinalizer interface {
	// soft=false (a BARE statement, POPFIN): always fire — a bare http.get(url) is fire-and-forget,
	// http.get(url).then(A) fires + runs A. soft=true (the value is ASSIGNED or RETURNED): fire ONLY
	// if a .then()/.catch() handler is attached (a "committed" request), so a plain lazy request held
	// in a variable stays lazy and fires on read. This closes the silent gaps where a handler attached
	// to an assigned/returned request would otherwise never run.
	FinalizeStatement(soft bool) (handler *Lambda, arg Value, run bool)
}
