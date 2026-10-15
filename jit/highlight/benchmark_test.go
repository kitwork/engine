package highlight

import (
	"strings"
	"testing"
)

// benchmarkSize is the size a language's samples are repeated to.
const benchmarkSize = 100 << 10

// BenchmarkTokenize has one sub-benchmark per language, on that language's
// samples repeated to ~100 KB, reported in bytes per second:
//
//	go test ./jit/highlight -run XXX -bench Tokenize/javascript -benchmem
func BenchmarkTokenize(b *testing.B) {
	for _, language := range Languages() {
		var samples []string
		for _, item := range fixturesFor(b, language) {
			samples = append(samples, item.source)
		}
		if len(samples) == 0 {
			b.Errorf("testdata/%s/ has no sample to benchmark", language)
			continue
		}
		source := repeatTo(strings.Join(samples, "\n"), benchmarkSize)
		tokenize := registry[language]
		b.Run(language, func(b *testing.B) {
			b.SetBytes(int64(len(source)))
			b.ReportAllocs()
			for b.Loop() {
				tokenizeWith(tokenize, source)
			}
		})
	}
}
