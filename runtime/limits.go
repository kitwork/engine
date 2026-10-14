package runtime

const (
	// MaxProgramBinarySize bounds the complete persisted Program envelope. Its debug table writes each
	// entry's file name, so a route bundle's envelope is about 15 times its code (apptop.vn 14/10/2026:
	// 993,905 bytes for 65,038): 64 MiB holds a Program at MaxBytecodeSize with room to spare.
	MaxProgramBinarySize = 64 << 20
	// MaxBytecodeSize is the largest verified instruction stream in one Program: 2 MiB (bytecode 3,
	// 14/10/2026; it was 65,535, the reach of a two-byte jump, and apptop.vn's largest route compiled
	// to 65,038). A jump in a program longer than MaxNarrowOperand is written wide (three bytes).
	MaxBytecodeSize = 2 << 20
	// MaxConstants is the largest immutable constant pool in one Program. An index above
	// MaxNarrowOperand is written wide; a program at MaxBytecodeSize references at most about half a
	// million constants, so the code limit is met first.
	MaxConstants = 1 << 20
	// MaxNarrowOperand is the largest constant index or jump address a two-byte operand holds. The
	// compiler writes the wide form of an instruction only past it.
	MaxNarrowOperand = 1<<16 - 1
	// maxWideOperand is the largest a three-byte operand holds.
	maxWideOperand = 1<<24 - 1
	// MaxStackDepth bounds the verifier-proven operand depth of one entry point.
	MaxStackDepth = 1<<16 - 1

	defaultMaxCallDepth              = 64
	defaultMaxEnergy          uint64 = 10_000_000
	cleanupEnergyReserve      uint64 = 10_000
	cancellationCheckInterval uint64 = 64
	cancellationCheckMask            = cancellationCheckInterval - 1
)

// A wide operand reaches every address and constant index these limits allow: raising a limit past
// three bytes is a compile error here, not a jump that silently lands short.
const (
	_ = uint32(maxWideOperand - MaxBytecodeSize)
	_ = uint32(maxWideOperand + 1 - MaxConstants)
)

// LimitsSnapshot is the immutable, detached resource policy enforced by the
// Program decoder, verifier, and VM. MaxEnergy remains host-configurable per
// execution; DefaultMaxEnergy is the host fallback, not an implicit VM limit.
type LimitsSnapshot struct {
	MaxProgramBinaryBytes     int    `json:"max_program_binary_bytes"`
	MaxBytecodeBytes          int    `json:"max_bytecode_bytes"`
	MaxConstants              int    `json:"max_constants"`
	MaxVerifiedStackDepth     int    `json:"max_verified_stack_depth"`
	MaxCallDepth              int    `json:"max_call_depth"`
	DefaultMaxEnergy          uint64 `json:"default_max_energy"`
	CleanupEnergyReserve      uint64 `json:"cleanup_energy_reserve"`
	CancellationCheckInterval uint64 `json:"cancellation_check_interval"`
}

// Limits returns a read-only policy snapshot. There is deliberately no
// mutation API: tenant code cannot widen the runtime's structural limits.
func Limits() LimitsSnapshot {
	return LimitsSnapshot{
		MaxProgramBinaryBytes:     MaxProgramBinarySize,
		MaxBytecodeBytes:          MaxBytecodeSize,
		MaxConstants:              MaxConstants,
		MaxVerifiedStackDepth:     MaxStackDepth,
		MaxCallDepth:              defaultMaxCallDepth,
		DefaultMaxEnergy:          defaultMaxEnergy,
		CleanupEnergyReserve:      cleanupEnergyReserve,
		CancellationCheckInterval: cancellationCheckInterval,
	}
}
