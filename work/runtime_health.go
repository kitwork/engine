package work

import (
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/site"
	"github.com/kitwork/engine/value"
)

// RuntimeHealth aggregates bounded process-local execution, request, render,
// cache, and generation signals. It intentionally records no request,
// argument, URL, or tenant data.
type RuntimeHealth struct {
	executions   atomic.Uint64
	successes    atomic.Uint64
	failures     atomic.Uint64
	instructions atomic.Uint64
	energy       atomic.Uint64

	maxInstructions atomic.Uint64
	maxEnergy       atomic.Uint64
	maxFrameDepth   atomic.Uint64

	requests                 atomic.Uint64
	completedRequests        atomic.Uint64
	successfulRequests       atomic.Uint64
	clientErrorRequests      atomic.Uint64
	serverErrorRequests      atomic.Uint64
	inflightRequests         atomic.Uint64
	maxInflightRequests      atomic.Uint64
	responseCacheHits        atomic.Uint64
	responseCacheMisses      atomic.Uint64
	preparedRenders          atomic.Uint64
	fallbackRenders          atomic.Uint64
	generationsPrepared      atomic.Uint64
	generationPrepErrors     atomic.Uint64
	generationsActivated     atomic.Uint64
	generationActErrors      atomic.Uint64
	generationDrainSuccesses atomic.Uint64
	generationDrainFailures  atomic.Uint64
	generationsPreparing     atomic.Uint64
	generationsActivating    atomic.Uint64
	maxDrainAge              atomic.Uint64
	programsDropped          atomic.Uint64

	requestLatency            latencyHistogram
	resolveLatency            latencyHistogram
	vmLatency                 latencyHistogram
	renderLatency             latencyHistogram
	generationPrepareLatency  latencyHistogram
	generationActivateLatency latencyHistogram
	generationDrainLatency    latencyHistogram

	mu          sync.RWMutex
	programs    map[[sha256.Size]byte]struct{}
	diagnostics map[runtime.DiagnosticCode]uint64

	lifecycleMu sync.RWMutex
	drains      map[*site.Generation]*generationDrainObservation
}

type generationDrainObservation struct {
	started    time.Time
	generation *site.Generation
}

const latencyBucketCount = 9
const maxObservedPrograms = 4096

var latencyBounds = [latencyBucketCount]time.Duration{
	100 * time.Microsecond,
	250 * time.Microsecond,
	500 * time.Microsecond,
	time.Millisecond,
	2500 * time.Microsecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	50 * time.Millisecond,
	250 * time.Millisecond,
}

type latencyHistogram struct {
	count    atomic.Uint64
	total    atomic.Uint64
	max      atomic.Uint64
	buckets  [latencyBucketCount]atomic.Uint64
	overflow atomic.Uint64
}

// LatencyBucketSnapshot is one exclusive histogram range. The lower bound is
// the preceding bucket's upper bound; Count is not cumulative.
type LatencyBucketSnapshot struct {
	UpperBoundMicroseconds uint64 `json:"upper_bound_microseconds"`
	Count                  uint64 `json:"count"`
}

// LatencySnapshot is bounded: its bucket count never depends on traffic,
// tenants, routes, or Program identities.
type LatencySnapshot struct {
	Count            uint64                  `json:"count"`
	TotalNanoseconds uint64                  `json:"total_nanoseconds"`
	MaxNanoseconds   uint64                  `json:"max_nanoseconds"`
	Buckets          []LatencyBucketSnapshot `json:"buckets"`
	Overflow         uint64                  `json:"overflow"`
}

type RequestHealthSnapshot struct {
	Started      uint64 `json:"started"`
	Completed    uint64 `json:"completed"`
	Successful   uint64 `json:"successful"`
	ClientErrors uint64 `json:"client_errors"`
	ServerErrors uint64 `json:"server_errors"`
	Inflight     uint64 `json:"inflight"`
	MaxInflight  uint64 `json:"max_inflight"`
}

type PresentationHealthSnapshot struct {
	PreparedRenders uint64 `json:"prepared_renders"`
	FallbackRenders uint64 `json:"fallback_renders"`
}

type ResponseCacheHealthSnapshot struct {
	Hits   uint64 `json:"hits"`
	Misses uint64 `json:"misses"`
}

// GenerationHealthSnapshot reports lifecycle observations. Drained and
// DrainFailures count completed observed drain attempts; they are not unique
// process-lifetime generation counts.
type GenerationHealthSnapshot struct {
	Prepared                  uint64 `json:"prepared"`
	PrepareFailures           uint64 `json:"prepare_failures"`
	Activated                 uint64 `json:"activated"`
	ActivateFailures          uint64 `json:"activate_failures"`
	Drained                   uint64 `json:"drained"`
	DrainFailures             uint64 `json:"drain_failures"`
	Preparing                 uint64 `json:"preparing"`
	Activating                uint64 `json:"activating"`
	Draining                  uint64 `json:"draining"`
	DrainingLeases            uint64 `json:"draining_leases"`
	OldestDrainNanoseconds    uint64 `json:"oldest_drain_nanoseconds"`
	MaxOldestDrainNanoseconds uint64 `json:"max_oldest_drain_nanoseconds"`
}

type LatencyHealthSnapshot struct {
	Request            LatencySnapshot `json:"request"`
	Resolve            LatencySnapshot `json:"resolve"`
	VM                 LatencySnapshot `json:"vm"`
	Render             LatencySnapshot `json:"render"`
	GenerationPrepare  LatencySnapshot `json:"generation_prepare"`
	GenerationActivate LatencySnapshot `json:"generation_activate"`
	GenerationDrain    LatencySnapshot `json:"generation_drain"`
}

// VMPoolHealthSnapshot reports process-global VM lease churn without exposing
// pooled VM instances or claiming an idle capacity that sync.Pool cannot know.
type VMPoolHealthSnapshot struct {
	Active   int64  `json:"active"`
	Created  uint64 `json:"created"`
	Acquired uint64 `json:"acquired"`
	Released uint64 `json:"released"`
}

// RuntimeHealthSnapshot is a stable, serializable point-in-time report.
type RuntimeHealthSnapshot struct {
	Executions      uint64                            `json:"executions"`
	Successes       uint64                            `json:"successes"`
	Failures        uint64                            `json:"failures"`
	Programs        int                               `json:"programs"`
	ProgramsDropped uint64                            `json:"programs_dropped"`
	Instructions    uint64                            `json:"instructions"`
	Energy          uint64                            `json:"energy"`
	MaxInstructions uint64                            `json:"max_instructions"`
	MaxEnergy       uint64                            `json:"max_energy"`
	MaxFrameDepth   uint64                            `json:"max_frame_depth"`
	Diagnostics     map[runtime.DiagnosticCode]uint64 `json:"diagnostics,omitempty"`
	Requests        RequestHealthSnapshot             `json:"requests"`
	Presentation    PresentationHealthSnapshot        `json:"presentation"`
	ResponseCache   ResponseCacheHealthSnapshot       `json:"response_cache"`
	Generations     GenerationHealthSnapshot          `json:"generations"`
	Latencies       LatencyHealthSnapshot             `json:"latencies"`
	VMPool          VMPoolHealthSnapshot              `json:"vm_pool"`

	LoadedApps                 int  `json:"loaded_apps"`
	LoadedSites                int  `json:"loaded_sites"`
	ActiveGenerations          int  `json:"active_generations"`
	ActiveGenerationLeases     int  `json:"active_generation_leases"`
	OwnershipSnapshotAvailable bool `json:"ownership_snapshot_available"`
}

func NewRuntimeHealth() *RuntimeHealth {
	return &RuntimeHealth{
		programs:    make(map[[sha256.Size]byte]struct{}),
		diagnostics: make(map[runtime.DiagnosticCode]uint64),
		drains:      make(map[*site.Generation]*generationDrainObservation),
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

	if program != nil {
		digest := program.ChecksumDigest()
		h.mu.RLock()
		_, exists := h.programs[digest]
		h.mu.RUnlock()
		if !exists {
			h.mu.Lock()
			if _, exists = h.programs[digest]; !exists {
				if len(h.programs) < maxObservedPrograms {
					h.programs[digest] = struct{}{}
				} else {
					h.programsDropped.Add(1)
				}
			}
			h.mu.Unlock()
		}
	}
	if diagnostic, ok := runtime.DiagnosticFrom(result); ok {
		h.mu.Lock()
		h.diagnostics[diagnostic.Code]++
		h.mu.Unlock()
	}

	if result.K == value.Invalid {
		h.failures.Add(1)
	} else {
		h.successes.Add(1)
	}
}

func (h *RuntimeHealth) RequestStarted() {
	if h == nil {
		return
	}
	h.requests.Add(1)
	current := h.inflightRequests.Add(1)
	updateAtomicMax(&h.maxInflightRequests, current)
}

func (h *RuntimeHealth) RequestCompleted(status int, elapsed time.Duration) {
	if h == nil {
		return
	}
	h.completedRequests.Add(1)
	h.inflightRequests.Add(^uint64(0))
	switch {
	case status >= 500:
		h.serverErrorRequests.Add(1)
	case status >= 400:
		h.clientErrorRequests.Add(1)
	default:
		h.successfulRequests.Add(1)
	}
	h.requestLatency.Record(elapsed)
}

func (h *RuntimeHealth) RecordResolve(elapsed time.Duration) {
	if h != nil {
		h.resolveLatency.Record(elapsed)
	}
}

func (h *RuntimeHealth) RecordVMLatency(elapsed time.Duration) {
	if h != nil {
		h.vmLatency.Record(elapsed)
	}
}

func (h *RuntimeHealth) RecordRender(elapsed time.Duration, prepared bool) {
	if h == nil {
		return
	}
	if prepared {
		h.preparedRenders.Add(1)
	} else {
		h.fallbackRenders.Add(1)
	}
	h.renderLatency.Record(elapsed)
}

func (h *RuntimeHealth) RecordResponseCache(hit bool) {
	if h == nil {
		return
	}
	if hit {
		h.responseCacheHits.Add(1)
	} else {
		h.responseCacheMisses.Add(1)
	}
}

func (h *RuntimeHealth) RecordGenerationPrepare(elapsed time.Duration, success bool) {
	if h == nil {
		return
	}
	if success {
		h.generationsPrepared.Add(1)
	} else {
		h.generationPrepErrors.Add(1)
	}
	h.generationPrepareLatency.Record(elapsed)
}

// BeginGenerationPrepare starts one in-progress preparation observation. The
// returned completion function is idempotent so deferred cleanup cannot
// underflow the gauge or record an outcome twice.
func (h *RuntimeHealth) BeginGenerationPrepare() func(bool) {
	if h == nil {
		return func(bool) {}
	}
	started := time.Now()
	h.generationsPreparing.Add(1)
	var once sync.Once
	return func(success bool) {
		once.Do(func() {
			h.generationsPreparing.Add(^uint64(0))
			h.RecordGenerationPrepare(time.Since(started), success)
		})
	}
}

func (h *RuntimeHealth) RecordGenerationActivate(elapsed time.Duration, success bool) {
	if h == nil {
		return
	}
	if success {
		h.generationsActivated.Add(1)
	} else {
		h.generationActErrors.Add(1)
	}
	h.generationActivateLatency.Record(elapsed)
}

// BeginGenerationActivate starts one in-progress activation observation.
func (h *RuntimeHealth) BeginGenerationActivate() func(bool) {
	if h == nil {
		return func(bool) {}
	}
	started := time.Now()
	h.generationsActivating.Add(1)
	var once sync.Once
	return func(success bool) {
		once.Do(func() {
			h.generationsActivating.Add(^uint64(0))
			h.RecordGenerationActivate(time.Since(started), success)
		})
	}
}

// RecordGenerationDrain records one successful observed drain attempt.
func (h *RuntimeHealth) RecordGenerationDrain(elapsed time.Duration) {
	if h == nil {
		return
	}
	h.recordGenerationDrain(elapsed, true)
}

// BeginGenerationDrain observes one generation only while its owner is
// synchronously draining it. Completion removes the internal observer before
// recording one attempt outcome and is idempotent. A concurrent observation
// of the same generation is a no-op while the first is registered. No
// unbounded tombstone is retained after completion, so these counters describe
// observed drain attempts, not process-lifetime unique generation identities.
func (h *RuntimeHealth) BeginGenerationDrain(generation *site.Generation) func(bool) {
	if h == nil || generation == nil || generation.Retired() {
		return func(bool) {}
	}
	observation := &generationDrainObservation{
		started:    time.Now(),
		generation: generation,
	}
	h.lifecycleMu.Lock()
	if h.drains == nil {
		h.drains = make(map[*site.Generation]*generationDrainObservation)
	}
	if _, exists := h.drains[generation]; exists {
		h.lifecycleMu.Unlock()
		return func(bool) {}
	}
	h.drains[generation] = observation
	h.lifecycleMu.Unlock()

	var once sync.Once
	return func(success bool) {
		once.Do(func() {
			elapsed := boundedElapsed(observation.started, time.Now())
			h.lifecycleMu.Lock()
			if h.drains[generation] == observation {
				delete(h.drains, generation)
			}
			observation.generation = nil
			h.lifecycleMu.Unlock()
			updateAtomicMax(&h.maxDrainAge, uint64(elapsed))
			h.recordGenerationDrain(elapsed, success)
		})
	}
}

func (h *RuntimeHealth) recordGenerationDrain(elapsed time.Duration, success bool) {
	if success {
		h.generationDrainSuccesses.Add(1)
	} else {
		h.generationDrainFailures.Add(1)
	}
	h.generationDrainLatency.Record(elapsed)
}

func (h *RuntimeHealth) Snapshot() RuntimeHealthSnapshot {
	if h == nil {
		return RuntimeHealthSnapshot{}
	}
	draining, drainingLeases, oldestDrain := h.generationDrainSnapshot(time.Now())
	updateAtomicMax(&h.maxDrainAge, oldestDrain)
	snapshot := RuntimeHealthSnapshot{
		Executions:      h.executions.Load(),
		Successes:       h.successes.Load(),
		Failures:        h.failures.Load(),
		Instructions:    h.instructions.Load(),
		Energy:          h.energy.Load(),
		MaxInstructions: h.maxInstructions.Load(),
		MaxEnergy:       h.maxEnergy.Load(),
		MaxFrameDepth:   h.maxFrameDepth.Load(),
		ProgramsDropped: h.programsDropped.Load(),
		Requests: RequestHealthSnapshot{
			Started:      h.requests.Load(),
			Completed:    h.completedRequests.Load(),
			Successful:   h.successfulRequests.Load(),
			ClientErrors: h.clientErrorRequests.Load(),
			ServerErrors: h.serverErrorRequests.Load(),
			Inflight:     h.inflightRequests.Load(),
			MaxInflight:  h.maxInflightRequests.Load(),
		},
		Presentation: PresentationHealthSnapshot{
			PreparedRenders: h.preparedRenders.Load(),
			FallbackRenders: h.fallbackRenders.Load(),
		},
		ResponseCache: ResponseCacheHealthSnapshot{
			Hits:   h.responseCacheHits.Load(),
			Misses: h.responseCacheMisses.Load(),
		},
		Generations: GenerationHealthSnapshot{
			Prepared:                  h.generationsPrepared.Load(),
			PrepareFailures:           h.generationPrepErrors.Load(),
			Activated:                 h.generationsActivated.Load(),
			ActivateFailures:          h.generationActErrors.Load(),
			Drained:                   h.generationDrainSuccesses.Load(),
			DrainFailures:             h.generationDrainFailures.Load(),
			Preparing:                 h.generationsPreparing.Load(),
			Activating:                h.generationsActivating.Load(),
			Draining:                  draining,
			DrainingLeases:            drainingLeases,
			OldestDrainNanoseconds:    oldestDrain,
			MaxOldestDrainNanoseconds: h.maxDrainAge.Load(),
		},
		Latencies: LatencyHealthSnapshot{
			Request:            h.requestLatency.Snapshot(),
			Resolve:            h.resolveLatency.Snapshot(),
			VM:                 h.vmLatency.Snapshot(),
			Render:             h.renderLatency.Snapshot(),
			GenerationPrepare:  h.generationPrepareLatency.Snapshot(),
			GenerationActivate: h.generationActivateLatency.Snapshot(),
			GenerationDrain:    h.generationDrainLatency.Snapshot(),
		},
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

func (h *RuntimeHealth) generationDrainSnapshot(now time.Time) (
	draining uint64,
	leases uint64,
	oldest uint64,
) {
	h.lifecycleMu.RLock()
	draining = uint64(len(h.drains))
	for _, observation := range h.drains {
		age := uint64(boundedElapsed(observation.started, now))
		if age > oldest {
			oldest = age
		}
		if observation.generation == nil {
			continue
		}
		active := observation.generation.Active()
		if active <= 0 {
			continue
		}
		leases = saturatingAdd(leases, uint64(active))
	}
	h.lifecycleMu.RUnlock()
	return draining, leases, oldest
}

func boundedElapsed(started, now time.Time) time.Duration {
	if started.IsZero() || now.Before(started) {
		return 0
	}
	return now.Sub(started)
}

func saturatingAdd(left, right uint64) uint64 {
	if ^uint64(0)-left < right {
		return ^uint64(0)
	}
	return left + right
}

func (h *latencyHistogram) Record(elapsed time.Duration) {
	if h == nil {
		return
	}
	if elapsed < 0 {
		elapsed = 0
	}
	nanoseconds := uint64(elapsed)
	h.count.Add(1)
	h.total.Add(nanoseconds)
	updateAtomicMax(&h.max, nanoseconds)
	for index, upper := range latencyBounds {
		if elapsed <= upper {
			h.buckets[index].Add(1)
			return
		}
	}
	h.overflow.Add(1)
}

func (h *latencyHistogram) Snapshot() LatencySnapshot {
	snapshot := LatencySnapshot{
		Count:            h.count.Load(),
		TotalNanoseconds: h.total.Load(),
		MaxNanoseconds:   h.max.Load(),
		Buckets:          make([]LatencyBucketSnapshot, latencyBucketCount),
		Overflow:         h.overflow.Load(),
	}
	for index, upper := range latencyBounds {
		snapshot.Buckets[index] = LatencyBucketSnapshot{
			UpperBoundMicroseconds: uint64(upper / time.Microsecond),
			Count:                  h.buckets[index].Load(),
		}
	}
	return snapshot
}

func updateAtomicMax(target *atomic.Uint64, candidate uint64) {
	for current := target.Load(); candidate > current; current = target.Load() {
		if target.CompareAndSwap(current, candidate) {
			return
		}
	}
}
