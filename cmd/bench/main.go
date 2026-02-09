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
	fmt.Println("Gen()      :", id.Gen())   // Default 36 chars Base36
	fmt.Println("Gen(6)     :", id.Gen(6))  // Smart Short (Seconds)
	fmt.Println("Gen(8)     :", id.Gen(8))  // Smart Short (Millis)
	fmt.Println("Gen(12)    :", id.Gen(12)) // Smart Medium (Millis + More Random)
	fmt.Println("Gen(30)    :", id.Gen(30)) // Smart Long (UnixNano)
	fmt.Println("-------------------")
	fmt.Println("Gen36()    :", id.Gen36())
	fmt.Println("Gen26()    :", id.Gen26())
	fmt.Println("Gen62()    :", id.Gen62())
	fmt.Println("Gen58()    :", id.Gen58())
	fmt.Println("Gen6()     :", id.Gen6())
	fmt.Println("Gen8()     :", id.Gen8())
	fmt.Println("Gen6_58()  :", id.Gen6_58())
	fmt.Println("Gen8_58()  :", id.Gen8_58())
	fmt.Println("-------------------")

	fmt.Printf("🚀 Starting Benchmark: %d goroutines generating Gener(8) concurrently...\n", total)
	start := time.Now()

	wg.Add(total)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()

			// Sinh ID 8 ký tự bằng hàm Smart Gen
			val := id.Gen(8)

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
