package core

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

func Source(source string) *Script {
	return &Script{
		Source: source,
	}
}

type Script struct {
	Source string
}

func (s *Script) Readfile() (string, error) {
	return readFile(s.Source)
}

func (s *Script) Content() (string, error) {
	if strings.HasSuffix(s.Source, ".js") {
		return readFile(s.Source)
	}
	return s.Source, nil
}

func (s *Script) Run(timeouts ...time.Duration) (value.Value, error) {
	content, err := s.Content()
	if err != nil {
		return value.Value{K: value.Invalid}, err
	}

	l := compiler.NewLexer(content)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return value.Value{K: value.Invalid}, fmt.Errorf("compile error: %s", p.Errors()[0])
	}

	stdlib := compiler.NewEnvironment()
	timeout := 6 * time.Second //
	if len(timeouts) > 0 {
		timeout = timeouts[0]
	}
	if timeout > 12*time.Second {
		timeout = 12 * time.Second
	}
	// ⏳ Tính năng chống treo hệ thống (Timeout Handling)
	if timeout > 0 {
		// Tạo channel để nhận kết quả từ goroutine thực thi
		done := make(chan value.Value, 1) // Buffer 1 để tránh goroutine rò rỉ nếu bị timeout
		errChan := make(chan error, 1)

		go func() {
			defer func() {
				// Bắt lỗi Panic nếu có trong lúc chạy script
				if r := recover(); r != nil {
					errChan <- fmt.Errorf("panic inside script evaluator: %v", r)
				}
			}()

			res := compiler.Evaluator(prog, stdlib)
			if res.IsInvalid() {
				errChan <- fmt.Errorf("runtime error during Evaluation")
			} else {
				done <- res
			}
		}()

		// Dùng Select để "đua" giữa kênh Trả-về và kênh Chờ-giờ
		select {
		case res := <-done:
			return res, nil
		case evalErr := <-errChan:
			return value.Value{K: value.Invalid}, evalErr
		case <-time.After(timeout):
			return value.Value{K: value.Invalid}, fmt.Errorf("script execution timed out after %v", timeout)
		}
	}

	// Chạy bình thường nếu không set Timeout
	res := compiler.Evaluator(prog, stdlib)
	if res.IsInvalid() {
		return value.Value{K: value.Invalid}, fmt.Errorf("runtime error during Evaluation")
	}
	return res, nil
}

func (s *Script) Blueprint() (*compiler.Bytecode, error) {
	content, err := s.Content()
	if err != nil {
		return nil, err
	}

	l := compiler.NewLexer(content)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("assemble error: %s", p.Errors()[0])
	}

	c := compiler.NewCompiler()
	if err := c.Compile(prog); err != nil {
		return nil, err
	}
	return c.ByteCodeResult(), nil
}

func (s *Script) Test(iterations int) (value.Value, error) {
	code, err := s.Content()
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

func readFile(source string) (string, error) {
	content, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
