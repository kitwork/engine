package engine

import (
	"fmt"
	"net/http"

	"github.com/kitwork/engine/core"
)

type Config struct {
	Port   int      `json:"port" yaml:"port"`
	Source string   `json:"source" yaml:"source"`
	Master []string `json:"master" yaml:"master"`
}

func Run(cfg *Config) {

	engine := core.New(cfg.Source)

	err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), engine)
	if err != nil {
		fmt.Println("Lỗi chạy server:", err)
	}
}
