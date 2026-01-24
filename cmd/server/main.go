package main

import (
	"github.com/kitwork/engine"
	"github.com/kitwork/engine/security"
)

func main() {
	// Load config from directory
	cfg, err := security.LoadConfigFromDir("./config")
	if err != nil {
		panic(err)
	}

	// Start Engine
	engine.Run(cfg)
}
