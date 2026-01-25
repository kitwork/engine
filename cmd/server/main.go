package main

import (
	"github.com/kitwork/engine"
)

func main() {
	cfg, err := engine.LoadConfig("./")
	if err != nil {
		cfg = &engine.Config{Port: 8081}
	}
	engine.Run(cfg)
}
