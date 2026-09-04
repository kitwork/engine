package snapshotfile

import (
	"errors"
	"os"
	"sync"
)

const maximumReadHandles = 16

// readHandlePool preserves io.ReaderAt semantics while avoiding a single
// operating-system handle as a concurrency bottleneck. Owners must drain all
// section readers before Close, matching Reader's existing lifetime contract.
type readHandlePool struct {
	files     []*os.File
	available chan *os.File
	closeOnce sync.Once
	closeErr  error
}

func newReadHandlePool(files []*os.File) *readHandlePool {
	pool := &readHandlePool{
		files: append([]*os.File(nil), files...), available: make(chan *os.File, len(files)),
	}
	for _, file := range pool.files {
		pool.available <- file
	}
	return pool
}

func (pool *readHandlePool) ReadAt(data []byte, offset int64) (int, error) {
	file := <-pool.available
	defer func() { pool.available <- file }()
	return file.ReadAt(data, offset)
}

func (pool *readHandlePool) Close() error {
	if pool == nil {
		return nil
	}
	pool.closeOnce.Do(func() {
		var failures []error
		for _, file := range pool.files {
			failures = append(failures, file.Close())
		}
		pool.closeErr = errors.Join(failures...)
	})
	return pool.closeErr
}

func closeReadHandles(files []*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}
