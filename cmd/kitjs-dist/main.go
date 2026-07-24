// kitjs-dist emits the publishable dist files for @kitwork/kitjs — the open-source, CDN-served
// build of the kernel (engine/jit/hydrate/runtime.js). The engine is the SINGLE source of truth:
// this command is the only sanctioned way to produce dist/, so the npm package can never drift
// from what the engine serves at /kit.js.
//
//	go run ./cmd/kitjs-dist <version> <outdir>
//	go run ./cmd/kitjs-dist 1.0.0 ../packages/kitjs/dist
package main

import (
	"fmt"
	"os"
	"path/filepath"

	hydrate "github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/utilities/minifier"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: kitjs-dist <version> <outdir>")
		os.Exit(1)
	}
	version, outdir := os.Args[1], os.Args[2]

	banner := "/*! @kitwork/kitjs v" + version + " | MIT | https://kitwork.io | " +
		"generated from engine/jit/hydrate/runtime.js — do not edit */\n"

	src := hydrate.Runtime()
	min := minifier.JS(src)
	if min == "" || len(min) >= len(src) {
		fmt.Fprintln(os.Stderr, "kitjs-dist: minification produced nothing smaller — refusing to write")
		os.Exit(1)
	}

	if err := os.MkdirAll(outdir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "kitjs-dist:", err)
		os.Exit(1)
	}
	write := func(name, body string) {
		path := filepath.Join(outdir, name)
		if err := os.WriteFile(path, []byte(banner+body), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "kitjs-dist:", err)
			os.Exit(1)
		}
		fmt.Printf("%s  %d bytes\n", path, len(banner)+len(body))
	}
	write("kitjs.js", src)
	write("kitjs.min.js", min)
}
