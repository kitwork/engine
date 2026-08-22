//go:build bleve_scale && !windows

package searchscale

import (
	"sync/atomic"
	"time"
)

func currentProcessRSS() (uint64, error) {
	return 0, nil
}

func sampleProcessRSS(peak *atomic.Uint64, stop <-chan struct{}, stopped chan<- struct{}) {
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
