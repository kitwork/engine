// vmcompat updates immutable VM compatibility evidence. It never runs as part
// of normal tests or engine startup.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kitwork/engine/compatibility"
)

func main() {
	update := flag.Bool("update", false, "compile sources and update compatibility evidence")
	replace := flag.Bool("replace", false, "allow replacement of an existing immutable archive")
	manifest := flag.String("manifest", compatibility.DefaultManifestPath, "archive manifest path")
	flag.Parse()
	if !*update {
		fmt.Fprintln(os.Stderr, "vmcompat: refusing to write without --update")
		os.Exit(2)
	}
	if err := compatibility.UpdateArchive(*manifest, *replace); err != nil {
		fmt.Fprintln(os.Stderr, "vmcompat:", err)
		os.Exit(1)
	}
	fmt.Println("vmcompat: updated", *manifest)
}
