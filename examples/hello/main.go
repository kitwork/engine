package main

import (
	"fmt"
	"os"

	"github.com/kitwork/engine"
)

func main() {
	if err := engine.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "kitwork:", err)
		os.Exit(1)
	}
}
