package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/kitwork/engine/jit/components"
	"github.com/kitwork/engine/jit/css"
)

// ============================================================================
// KITWORK INDUSTRIAL SYSTEM (v15.2) - UTILITY BUILDER
// ============================================================================

func main() {
	htmlPath := "demo/view/work.html"
	html, err := ioutil.ReadFile(htmlPath)
	if err != nil {
		fmt.Printf("Error reading HTML file: %v\n", err)
		return
	}

	// 1. Generate Static Framework (Complete Table)
	framework := css.GenerateFramework()

	// 2. Generate JIT Utilities from HTML Usage
	jit := css.GenerateJIT(string(html))

	// 3. Write Outputs
	_ = ioutil.WriteFile("demo/public/css/framework.css", []byte(framework), 0644)
	_ = ioutil.WriteFile("demo/public/css/jit.css", []byte(jit), 0644)

	// 4. Report
	jitCount := len(strings.Split(jit, "}\n")) - 1
	// 4. GENERATE COMPONENTS (Modular Library)
	os.MkdirAll("demo/public/css/components", 0755)

	library := components.GenerateLibrary()
	var totalCompBytes int
	for filename, content := range library {
		path := fmt.Sprintf("demo/public/css/components/%s", filename)
		_ = os.WriteFile(path, []byte(content), 0644)
		totalCompBytes += len(content)
		// fmt.Printf("  + %s (%d bytes)\n", filename, len(content))
	}
	fmt.Printf("COMP: %d bytes (%d files)\n", totalCompBytes, len(library))

	fmt.Printf("\n--- Kitwork Industrial System v15.2 (Complete Table) ---\n")
	fmt.Printf("FW: %d bytes | JIT: %d classes generated\n", len(framework), jitCount)
	fmt.Println("--- Sovereign Engine: Build Complete ---")
}
