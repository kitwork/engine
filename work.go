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

	engine := core.New(cfg.Source)

	err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), engine)
	if err != nil {
		fmt.Println("Lỗi chạy server:", err)
	}
}
