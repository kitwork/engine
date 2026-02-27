package main

import (
	"fmt"

	"github.com/kitwork/engine/script"
)

func main() {
	source := `
			let a = 1;
			let b = 2;
			return a + b;
		`
	// Hoặc gán built-in nếu bạn muốn chạy kèm các thư viện: engine.Builtins("localhost")
	res, err := script.Test(source, 1_000_000)
	if err != nil {
		fmt.Println("Lỗi:", err)
	} else {
		fmt.Println("Kết quả là:", res.Interface())
	}
}

// engine := core.New("public")
