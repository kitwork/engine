package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/kitwork/engine/id"
)

func main() {
	const total = 1_000_000 // 1 triệu lần
	var wg sync.WaitGroup
	var ids sync.Map // Map an toàn cho concurrency để check trùng

	// In mẫu ID
	fmt.Println("--- ID EXAMPLES ---")
	fmt.Println("Gen36()    :", id.Charset(36).Must(36))
	fmt.Println("Gen26()    :", id.Charset(26).Must(26))
	fmt.Println("Gen62()    :", id.Charset(62).Must(62))
	fmt.Println("Gen58()    :", id.Charset(58).Must(58))
	fmt.Println("Gen8()     :", id.Charset(62).Must(8))

	fmt.Println("-------------------")

	fmt.Printf("🚀 Starting Benchmark: %d goroutines generating Gener(8) concurrently...\n", total)
	start := time.Now()

	wg.Add(total)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()

			// Sinh ID 8 ký tự bằng hàm Smart Gen
			val := id.Shortlink()

			// Kiểm tra trùng (Store trả về true nếu đã có key)
			if _, loaded := ids.LoadOrStore(val, true); loaded {
				fmt.Printf("❌ DUPLICATE FOUND: %s\n", val)
			}
		}()
	}

	wg.Wait()
	duration := time.Since(start)

	// Đếm số lượng ID thực tế trong Map
	count := 0
	ids.Range(func(key, value interface{}) bool {
		count++
		return true
	})

	fmt.Printf("\n✅ Finished in %v\n", duration)
	fmt.Printf("📊 Total Generated: %d\n", total)
	fmt.Printf("🔍 Unique IDs:      %d\n", count)

	if count == total {
		fmt.Println("🎉 SUCCESS: No duplicates found!")
	} else {
		fmt.Printf("💀 FAILURE: %d duplicates found!\n", total-count)
	}
}
