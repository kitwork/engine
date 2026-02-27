package engine

import (
	"fmt"
	"net/http"

	"github.com/kitwork/engine/core"
)

type Config struct {
	Port   int
	Source string
	Master []string
}

func Run(cfg *Config) {
	fmt.Printf("Bắt đầu khởi động Engine tại cổng %d, dùng thư mục: %s\n", cfg.Port, cfg.Source)
	fmt.Println("Truy cập thử: http://localhost:3000/test")

	engine := core.New(cfg.Source)

	err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), engine)
	if err != nil {
		fmt.Println("Lỗi chạy server:", err)
	}
}
