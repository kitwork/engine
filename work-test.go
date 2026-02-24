package engine

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/kitwork/engine/core"
)

// Test thực thi script với số vòng lặp tùy chọn (mặc định là 1) và báo cáo hiệu năng chi tiết (hỗ trợ đa luồng)
func Test(source string, iterations ...int) {
	iters := 1
	if len(iterations) > 0 {
		iters = iterations[0]
	}

	e := core.New()
	e.Config.Debug = false
	w, err := e.Build(source, "", "", "")
	if err != nil {
		fmt.Printf("❌ Build Error: %v\n", err)
		return
	}

	// Cấu hình worker count dựa trên tài nguyên hệ thống (GOMAXPROCS an toàn hơn NumCPU)
	workerCount := runtime.GOMAXPROCS(0)
	if iters < workerCount {
		workerCount = iters
	}

	// Chuẩn bị đo lường
	var msBefore, msAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&msBefore)
	start := time.Now()

	// Chạy song song (Parallel execution)
	var wg sync.WaitGroup
	wg.Add(workerCount)
	itersPerWorker := iters / workerCount
	ctx := context.Background()

	for i := 0; i < workerCount; i++ {
		// Xử lý nốt các vòng lặp dư nếu iters không chia hết cho workerCount
		count := itersPerWorker
		if i == workerCount-1 {
			count = iters - (itersPerWorker * (workerCount - 1))
		}

		go func(c int) {
			defer wg.Done()
			for j := 0; j < c; j++ {
				res := e.Trigger(ctx, w, nil, nil)
				if res.Error != "" {
					// Chỉ in lỗi một vài lần để tránh làm chậm throughput do I/O log
					if j%1000 == 0 {
						fmt.Printf("⚠️ Lỗi tại worker: %v\n", res.Error)
					}
				}
			}
		}(count)
	}
	wg.Wait()

	duration := time.Since(start)
	runtime.ReadMemStats(&msAfter)

	// Tính toán các chỉ số
	throughput := float64(iters) / duration.Seconds()
	if duration.Seconds() == 0 {
		throughput = 0
	}
	latency := duration / time.Duration(iters)
	totalAlloc := msAfter.TotalAlloc - msBefore.TotalAlloc
	gcCycles := msAfter.NumGC - msBefore.NumGC

	// In kết quả theo format yêu cầu
	fmt.Printf("\n"+
		"====================================================\n"+
		"          🚀 ENGINE STRESS TEST (PARALLEL & POOLED)  \n"+
		"====================================================\n"+
		"       ⚡ PERFORMANCE METRICS\n"+
		"----------------------------------------------------\n"+
		"Total Iterations : %d\n"+
		"Total Duration   : %v\n"+
		"Throughput      : %.0f ops/sec\n"+
		"Avg Latency     : %v\n"+
		"\n"+
		"       ⚙️ SYSTEM & CONCURRENCY\n"+
		"----------------------------------------------------\n"+
		"Workers (Cores) : %d\n"+
		"\n"+
		"       💾 MEMORY & GC STATS\n"+
		"----------------------------------------------------\n"+
		"Alloc per Op    : %d bytes\n"+
		"Total Allocated : %.2f MB\n"+
		"GC Cycles       : %d\n"+
		"====================================================\n",
		iters, duration, throughput, latency,
		workerCount,
		totalAlloc/uint64(iters), float64(totalAlloc)/1024/1024,
		gcCycles)
}
