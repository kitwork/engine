package highlight

import (
	"strings"
	"testing"
)

// FuzzTokenize feeds arbitrary bytes to every language, unguarded, and holds
// each result to the contract. A plain `go test` runs the seeds (every sample,
// every hostile fragment alone and three times over); `go test -run XXX -fuzz
// FuzzTokenize` mutates from them. A failing input lands in
// testdata/fuzz/FuzzTokenize/ and becomes a seed from then on.
func FuzzTokenize(f *testing.F) {
	for _, item := range loadFixtures(f) {
		f.Add(item.source)
	}
	for _, fragment := range hostileFragments {
		f.Add(fragment)
		f.Add(strings.Repeat(fragment, 3))
	}
	languages := Languages()
	f.Fuzz(func(t *testing.T, source string) {
		for _, language := range languages {
			if problem := contractViolation(source, tokenizeWith(registry[language], source)); problem != "" {
				t.Fatalf("%s: %s\nsource: %q", language, problem, source)
			}
		}
	})
}
