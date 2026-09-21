package hydrate

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// kernelCoreBudget is the spec's bundle budget (ideaship-final §8, ideaship-master §6): the core
// kernel at or under 12 KB gzip, checked in CI. The pipeline ships kernel.js as written — there is
// no minifier — so the figure gated here is the kernel with its comment lines and indentation
// removed and gzipped at the best level, i.e. the bytes a minifier would keep. The modules that
// ride with it (bridge, morph, Drive, capability modules, boot) are outside the core figure.
const kernelCoreBudget = 12 * 1024

func kernelCoreBytes() []byte {
	var kept []string
	for _, line := range strings.Split(kernelJS, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		kept = append(kept, trimmed)
	}
	return []byte(strings.Join(kept, "\n"))
}

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
	core := kernelCoreBytes()
	size := gzipSize(t, core)
	t.Logf("kernel core: %d bytes, %d bytes gzip (budget %d)", len(core), size, kernelCoreBudget)
	if size > kernelCoreBudget {
		t.Fatalf("the kernel core is %d bytes gzip, over the %d-byte budget of the spec — trim before adding", size, kernelCoreBudget)
	}
	// The stripper must be doing its job, or the gate measures the wrong thing.
	if len(core) >= len(kernelJS) || !strings.Contains(kernelJS, "// ") {
		t.Fatal("the core measure should be the kernel without its comment lines")
	}
}
