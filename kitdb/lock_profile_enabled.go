//go:build kitdb_profile

package kitdb

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/internal/benchclock"
)

const lockProfileCapacity = 4096

// LockProfileSample uses monotonic nanoseconds relative to this DB's profile
// epoch. It records wall intervals, including scheduling and GC, not CPU time.
type LockProfileSample struct {
	Site        string   `json:"site"`
	Transaction uint64   `json:"transaction"`
	Start       int64    `json:"start_ns"`
	Acquired    int64    `json:"acquired_ns"`
	Release     int64    `json:"release_ns"`
	Unlocked    int64    `json:"unlocked_ns"`
	Phases      [3]int64 `json:"snapshot_mu_open_clone_ns"`
}

type LockProfileReport struct {
	Capacity int                 `json:"capacity"`
	Dropped  uint64              `json:"dropped"`
	Samples  []LockProfileSample `json:"samples"`
}

type lockProfile struct {
	enabled atomic.Bool
	mu      sync.Mutex
	epoch   benchclock.Stamp
	samples []LockProfileSample
	dropped uint64
}

type lockStamp = benchclock.Stamp
type lockSpan struct {
	owner  *lockProfile
	sample LockProfileSample
}

// EnableLockProfile is diagnostic-build-only. Enable once, before launching
// workload goroutines; allocation and epoch publication stay outside DB locks.
func (db *DB) EnableLockProfile() error {
	p := &db.lockProfile
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.enabled.Load() {
		return fmt.Errorf("kitdb: lock profile already enabled")
	}
	p.samples = make([]LockProfileSample, 0, lockProfileCapacity)
	p.epoch = benchclock.Now()
	p.enabled.Store(true)
	return nil
}

// LockProfile copies bounded evidence. Read after workload goroutines stop;
// concurrent reads are safe but may omit intervals which have not ended yet.
func (db *DB) LockProfile() LockProfileReport {
	p := &db.lockProfile
	p.mu.Lock()
	defer p.mu.Unlock()
	return LockProfileReport{Capacity: lockProfileCapacity, Dropped: p.dropped,
		Samples: append([]LockProfileSample(nil), p.samples...)}
}

func (p *lockProfile) start(site lockSite) lockSpan {
	if !p.enabled.Load() {
		return lockSpan{}
	}
	names := [...]string{"snapshot", "snapshot.reserve", "snapshot.capture", "checkpoint.read", "checkpoint.install", "checkpoint.compact", "wal.detach", "wal.install"}
	return lockSpan{owner: p, sample: LockProfileSample{Site: names[site], Start: benchclock.Since(p.epoch).Nanoseconds()}}
}

func (s *lockSpan) acquired() {
	if s.owner != nil {
		s.sample.Acquired = benchclock.Since(s.owner.epoch).Nanoseconds()
	}
}

func (s *lockSpan) releasing() {
	if s.owner != nil {
		s.sample.Release = benchclock.Since(s.owner.epoch).Nanoseconds()
	}
}

func (s *lockSpan) transaction(tx uint64) {
	if s.owner != nil {
		s.sample.Transaction = tx
	}
}

func (s *lockSpan) mark() lockStamp {
	if s.owner != nil {
		return benchclock.Now()
	}
	return 0
}

func (s *lockSpan) phase(index int, start lockStamp) {
	if s.owner != nil {
		s.sample.Phases[index] += benchclock.Since(start).Nanoseconds()
	}
}

func (s *lockSpan) end() {
	if s.owner == nil {
		return
	}
	p := s.owner
	s.sample.Unlocked = benchclock.Since(p.epoch).Nanoseconds()
	// Collection never holds db.mu and never calls trace/formatting/file I/O.
	p.mu.Lock()
	if len(p.samples) < cap(p.samples) {
		p.samples = append(p.samples, s.sample)
	} else {
		p.dropped++
	}
	p.mu.Unlock()
	s.owner = nil
}
