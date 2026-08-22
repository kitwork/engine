//go:build scale && !windows

package search

import (
	"sync/atomic"
	"time"
)

func currentSegmentScaleRSS() (uint64, error) {
	return 0, nil
}

func sampleSegmentScaleRSS(_ *atomic.Uint64, stop <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-stop:
			return
		}
	}
}
