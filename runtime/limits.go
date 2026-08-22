package runtime

const (
	// MaxProgramBinarySize bounds the complete persisted Program envelope.
	MaxProgramBinarySize = 16 << 20
	// MaxBytecodeSize is the largest verified instruction stream in one Program.
	MaxBytecodeSize = 1<<16 - 1
	// MaxConstants is the largest immutable constant pool in one Program.
	MaxConstants = 1 << 16
	// MaxStackDepth bounds the verifier-proven operand depth of one entry point.
	MaxStackDepth = 1<<16 - 1

	defaultMaxCallDepth              = 64
	defaultMaxEnergy          uint64 = 10_000_000
	cleanupEnergyReserve      uint64 = 10_000
	cancellationCheckInterval uint64 = 64
	cancellationCheckMask            = cancellationCheckInterval - 1
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
