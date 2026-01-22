package engine

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
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
		// Đăng ký biến db vào stdlib của engine để JS có thể dùng
		e.stdlib.Set("db", value.New(&Database{}))

		source := `
			let query = db.from("users").limit(50).get();
			query;
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

	iterations := 1000000 // 1 Triệu lần chạy
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
	opsPerSec := float64(iterations) / duration.Seconds()
	fmt.Printf("Tổng số iterations: %d\n", iterations)
	fmt.Printf("Tổng thời gian:     %v\n", duration)
	fmt.Printf("Thời gian/op:       %v\n", duration/time.Duration(iterations))
	fmt.Printf("Throughput:         %.0f ops/sec (VM Execution)\n", opsPerSec)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("RAM hiện tại:       %v MB\n", m.Alloc/1024/1024)
	fmt.Println("--------------------------------------------------")
}
