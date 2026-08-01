package work

import (
	"crypto/sha256"
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// RuntimeHealth aggregates process-local VM execution signals. It intentionally
// records no request, argument, URL, or tenant data.
type RuntimeHealth struct {
	executions   atomic.Uint64
	successes    atomic.Uint64
	failures     atomic.Uint64
	instructions atomic.Uint64
	energy       atomic.Uint64

	maxInstructions atomic.Uint64
	maxEnergy       atomic.Uint64
	maxFrameDepth   atomic.Uint64

	mu          sync.RWMutex
	programs    map[[sha256.Size]byte]struct{}
	diagnostics map[runtime.DiagnosticCode]uint64
}

// RuntimeHealthSnapshot is a stable, serializable point-in-time report.
type RuntimeHealthSnapshot struct {
	Executions      uint64                            `json:"executions"`
	Successes       uint64                            `json:"successes"`
	Failures        uint64                            `json:"failures"`
	Programs        int                               `json:"programs"`
	Instructions    uint64                            `json:"instructions"`
	Energy          uint64                            `json:"energy"`
	MaxInstructions uint64                            `json:"max_instructions"`
	MaxEnergy       uint64                            `json:"max_energy"`
	MaxFrameDepth   uint64                            `json:"max_frame_depth"`
	Diagnostics     map[runtime.DiagnosticCode]uint64 `json:"diagnostics,omitempty"`
}

func NewRuntimeHealth() *RuntimeHealth {
	return &RuntimeHealth{
		programs:    make(map[[sha256.Size]byte]struct{}),
		diagnostics: make(map[runtime.DiagnosticCode]uint64),
	}
}

func (h *RuntimeHealth) Record(
	program *runtime.Program,
	stats runtime.VMStats,
	result value.Value,
) {
	if h == nil {
		return
	}
	h.executions.Add(1)
	h.instructions.Add(stats.Instructions)
	h.energy.Add(stats.Energy)
	updateAtomicMax(&h.maxInstructions, stats.Instructions)
	updateAtomicMax(&h.maxEnergy, stats.Energy)
	updateAtomicMax(&h.maxFrameDepth, uint64(stats.PeakFrameDepth))

	h.mu.Lock()
	if program != nil {
		h.programs[program.ChecksumDigest()] = struct{}{}
	}
	if diagnostic, ok := runtime.DiagnosticFrom(result); ok {
		h.diagnostics[diagnostic.Code]++
	}
	h.mu.Unlock()

	if result.K == value.Invalid {
		h.failures.Add(1)
	} else {
		h.successes.Add(1)
	}
}

func (h *RuntimeHealth) Snapshot() RuntimeHealthSnapshot {
	if h == nil {
		return RuntimeHealthSnapshot{}
	}
	snapshot := RuntimeHealthSnapshot{
		Executions:      h.executions.Load(),
		Successes:       h.successes.Load(),
		Failures:        h.failures.Load(),
		Instructions:    h.instructions.Load(),
		Energy:          h.energy.Load(),
		MaxInstructions: h.maxInstructions.Load(),
		MaxEnergy:       h.maxEnergy.Load(),
		MaxFrameDepth:   h.maxFrameDepth.Load(),
	}
	h.mu.RLock()
	snapshot.Programs = len(h.programs)
	if len(h.diagnostics) > 0 {
		snapshot.Diagnostics = make(map[runtime.DiagnosticCode]uint64, len(h.diagnostics))
		for code, count := range h.diagnostics {
			snapshot.Diagnostics[code] = count
		}
	}
	h.mu.RUnlock()
	return snapshot
}

func updateAtomicMax(target *atomic.Uint64, candidate uint64) {
	for current := target.Load(); candidate > current; current = target.Load() {
		if target.CompareAndSwap(current, candidate) {
			return
		}
	}
}
