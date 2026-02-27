package script

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

func Test(source string, iterations int) (value.Value, error) {
	return New().Test(source, iterations)
}

func (s *Script) Test(source string, iterations int) (value.Value, error) {
	code, err := s.code(source)
	if err != nil {
		return value.Value{K: value.Invalid}, err
	}

	l := compiler.NewLexer(code)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return value.Value{K: value.Invalid}, fmt.Errorf("compile error: %s", p.Errors()[0])
	}

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	opsPerWorker := iterations / workers
	remainingOps := iterations % workers

	var lastResult value.Value
	var mu sync.Mutex
	var runErr error

	startEval := time.Now()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			count := opsPerWorker
			if workerID == 0 {
				count += remainingOps
			}

			var localRes value.Value

			// ⚡ OPTIMIZATION: Khởi tạo Environment một lần duy nhất cho mỗi Worker.
			// Tránh việc bộ nhớ (Heap) phải cấp phát map[string]value.Value hàng triệu lần.
			stdlib := compiler.NewEnvironment()

			for i := 0; i < count; i++ {
				// Reset() sẽ xóa key nhưng giữ nguyên sức chứa (capacity) của Map,
				// giúp tái sử dụng vùng nhớ cũ, đẩy lượng rác (GC) về mức cực thấp.
				stdlib.Reset()

				localRes = compiler.Evaluator(prog, stdlib)
				if localRes.IsInvalid() {
					mu.Lock()
					runErr = fmt.Errorf("runtime error during execution")
					mu.Unlock()
					return
				}
			}

			if workerID == 0 {
				mu.Lock()
				lastResult = localRes
				mu.Unlock()
			}
		}(w)
	}

	wg.Wait()
	evalTime := time.Since(startEval)

	if runErr != nil {
		return value.Value{K: value.Invalid}, runErr
	}

	runtime.ReadMemStats(&m2)

	allocBytes := m2.TotalAlloc - m1.TotalAlloc
	gcCycles := m2.NumGC - m1.NumGC

	ops := float64(iterations) / evalTime.Seconds()
	avgLatency := evalTime.Nanoseconds() / int64(iterations)

	allocPerOp := uint64(0)
	if iterations > 0 {
		allocPerOp = allocBytes / uint64(iterations)
	}
	totalAllocMB := float64(allocBytes) / 1024 / 1024

	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("      🚀 ENGINE STRESS TEST (SAFE & POOLED)")
	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("      ⚡ PERFORMANCE METRICS")
	fmt.Println(strings.Repeat("-", 45))
	fmt.Printf("Total Iterations : %d\n", iterations)
	fmt.Printf("Total Duration   : %v\n", evalTime)
	fmt.Printf("Throughput       : %.0f ops/sec\n", ops)
	fmt.Printf("Avg Latency      : %dns\n", avgLatency)
	fmt.Println()
	fmt.Println("      ⚙️ SYSTEM & CONCURRENCY")
	fmt.Println(strings.Repeat("-", 45))
	fmt.Printf("Workers          : %d\n", workers)
	fmt.Println()
	fmt.Println("      💾 MEMORY & GC STATS")
	fmt.Println(strings.Repeat("-", 45))
	fmt.Printf("Alloc per Op     : %d bytes\n", allocPerOp)
	fmt.Printf("Total Allocated  : %.2f MB\n", totalAllocMB)
	fmt.Printf("GC Cycles        : %d\n", gcCycles)
	fmt.Println(strings.Repeat("=", 45))

	return lastResult, nil
}
