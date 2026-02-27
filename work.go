package engine

import (
	"fmt"

	"github.com/kitwork/engine/script"
)

type Config struct {
	Port   int
	Source string
	Master []string
}

func Run(cfg *Config) {

	// engine := core.New("public")

	source := `
			let a = 1;
			let b = 2;
			return a + b;
		`
	// Hoặc gán built-in nếu bạn muốn chạy kèm các thư viện: engine.Builtins("localhost")
	res, err := script.Stress(source, 1_000_000)
	if err != nil {
		fmt.Println("Lỗi:", err)
	} else {
		// Sẽ in ra: "Kết quả là: 300"
		fmt.Println("Kết quả là:", res.Interface())
	}
}
