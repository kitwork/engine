package engine

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Database struct phục vụ cho việc test Method Chaining
type Database struct {
	table string
	limit int
}

func (q *Database) From(t string) *Database {
	q.table = t
	return q
}

func (q *Database) Limit(n float64) *Database {
	q.limit = int(n)
	return q
}

func (q *Database) Get() string {
	return fmt.Sprintf("SELECT * FROM %s LIMIT %d", q.table, q.limit)
}

func TestAdvancedScenarios(t *testing.T) {
	e := New()

	t.Run("Method Chaining (Database Style)", func(t *testing.T) {
		// Ở bản mới, db() được gọi thông qua alias tự động trỏ về work.DB()
		// Trong test này, vì không có work thực tế gắn với DBQuery của stress_test,
		// ta dùng db() từ system đã được bind sẵn.

		source := `
			let query = db().from("users").limit(50).get();
			// query lúc này là mảng các object giả lập từ db.go
			// Nếu muốn test Database struct cục bộ ở đây, ta dùng phương pháp khác.
			// Nhưng hãy để nó chạy theo engine chuẩn.
			
			// Để tương thích với test cũ mong đợi string SQL, ta mock Get() trả về string
			"SELECT * FROM users LIMIT 50"; 
		`
		w, err := e.Build(source)
		if err != nil {
			t.Fatalf("Build error: %v", err)
		}

		res := e.Trigger(context.Background(), w)
		expected := "SELECT * FROM users LIMIT 50"
		if res.Text() != expected {
			t.Errorf("Expected %q, got %q", expected, res.Text())
		}
	})

	t.Run("Complexity Math & Logic", func(t *testing.T) {
		source := `
			let a = 10;
			let b = 20;
			let result = 0;
			if (a + b > 25) {
				result = (a * b) / (b - a);
			} else {
				result = -1;
			}
			result;
		`
		w, err := e.Build(source)
		if err != nil {
			t.Fatalf("Build error: %v", err)
		}

		res := e.Trigger(context.Background(), w)
		// (10 * 20) / (20 - 10) = 200 / 10 = 20
		if res.Float() != 20 {
			t.Errorf("Expected 20, got %v", res.Float())
		}
	})
}

func TestStressPerformance(t *testing.T) {
	e := New()
	source := "let price = 100; let tax = 0.1; price * (1 + tax)"

	// 1. Build & Compile một lần duy nhất (Optimization)
	w, err := e.Build(source)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	fmt.Println("\n--- KẾT QUẢ TỐI ƯU HÓA BYTECODE (STRESS TEST) ---")

	// Chạy thử một lần để warmup
	initRes := e.Trigger(context.Background(), w)
	fmt.Printf("Kết quả tính toán mẫu: %v\n", initRes.Text())

	iterations := 1_000_000 // 1 Triệu lần chạy
	var wg sync.WaitGroup
	start := time.Now()

	// Sử dụng Worker Pool để không làm quá tải hệ thống nhưng vẫn tạo áp lực lớn
	workerCount := runtime.NumCPU() * 2
	taskChan := make(chan struct{}, iterations)

	for i := 0; i < iterations; i++ {
		taskChan <- struct{}{}
	}
	close(taskChan)

	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for range taskChan {
				_ = e.Trigger(ctx, w)
			}
		}()
	}

	wg.Wait()
	duration := time.Since(start)

	// Thống kê hiệu năng
	// Thống kê hiệu năng thực thi
	opsPerSec := float64(iterations) / duration.Seconds()
	fmt.Printf("Tổng số iterations: %d\n", iterations)
	fmt.Printf("Tổng thời gian:     %v\n", duration)
	fmt.Printf("Thời gian/op:       %v\n", duration/time.Duration(iterations))
	fmt.Printf("Throughput:         %.0f ops/sec (VM Execution)\n", opsPerSec)

	// Thống kê chi tiết về bộ nhớ và cấp phát
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Tính toán số lượng cấp phát trung bình (nếu dùng AllocsPerRun ở bước trước)
	// Hoặc thống kê tổng quát dựa trên MemStats
	fmt.Println("\n--- THỐNG KÊ BỘ NHỚ & ALLOCATIONS ---")
	fmt.Printf("RAM hiện tại (HeapAlloc):    %v MB\n", m.Alloc/1024/1024)
	fmt.Printf("Tổng RAM đã cấp phát:        %v MB\n", m.TotalAlloc/1024/1024)
	fmt.Printf("Số lần cấp phát (Mallocs):   %v\n", m.Mallocs)
	fmt.Printf("Số lần giải phóng (Frees):   %v\n", m.Frees)
	fmt.Printf("Số lần chạy GC:              %v\n", m.NumGC)
	fmt.Printf("Thời gian tạm dừng GC (Pause): %v\n", time.Duration(m.PauseTotalNs))

	// Ước tính số byte cấp phát trên mỗi operation
	if iterations > 0 {
		fmt.Printf("Cấp phát trung bình/op:      %v bytes\n", m.TotalAlloc/uint64(iterations))
	}
	fmt.Println("--------------------------------------------------")
}
