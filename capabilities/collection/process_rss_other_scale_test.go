//go:build scale && !windows

package collection

import (
	"sync/atomic"
	"time"
)

func currentCollectionProcessRSS() (uint64, error) {
	return 0, nil
}

func sampleCollectionProcessRSS(peak *atomic.Uint64, stop <-chan struct{}, stopped chan<- struct{}) {
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
