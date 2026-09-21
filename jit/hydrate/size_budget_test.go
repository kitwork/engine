//go:build !stdminify

package hydrate

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/kitwork/engine/utilities/minifier"
)

// kernelCoreBudget is the spec's bundle budget (ideaship-final §8, ideaship-master §6): the core
// kernel at or under 12 KB gzip, checked in CI. Production serves /kit.js through minifier.JS
// (work/jithydrate.go), so the figure gated here is kernel.js minified the same way and gzipped at
// the best level — the bytes a visitor's browser actually receives for the core. The modules that
// ride with it (bridge, morph, Drive, capability modules, boot) are outside the core figure. The
// stdminify build serves readable source and is not gated (its minifier is a pass-through).
const kernelCoreBudget = 12 * 1024

func gzipSize(t *testing.T, source []byte) int {
	t.Helper()
	var out bytes.Buffer
	writer, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(source); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Len()
}

func TestKernelCoreStaysWithinTheGzipBudget(t *testing.T) {
	core := minifier.JS(kernelJS)
	if len(core) >= len(kernelJS) {
		t.Fatal("the minifier returned the kernel unchanged — the gate would measure readable source")
	}
	size := gzipSize(t, []byte(core))
	t.Logf("kernel core: %d bytes minified, %d bytes gzip (budget %d)", len(core), size, kernelCoreBudget)
	if size > kernelCoreBudget {
		t.Fatalf("the kernel core is %d bytes gzip, over the %d-byte budget of the spec — trim before adding", size, kernelCoreBudget)
	}
}
