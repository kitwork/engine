package runtime

// Cost is one unit of VM execution energy.
type Cost uint64

// cleanupEnergyReserve is the total energy available to deferred cleanup after
// normal execution exhausts its budget. All defers in the unwind share it.
const cleanupEnergyReserve uint64 = 10_000

// Table remains the dispatch fast path, but InstructionSpec is the single
// source of truth for energy costs.
var Table = func() [256]Cost {
	var table [256]Cost
	for op, spec := range instructionTable {
		table[op] = spec.Energy
	}
	return table
}()
