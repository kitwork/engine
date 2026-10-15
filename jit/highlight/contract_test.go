package highlight

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Every language= value has a sample, and every sample directory is a language:
// a sample is what the contract tests, the fuzz seeds and the benchmark run on.
func TestEveryLanguageHasASample(t *testing.T) {
	for _, language := range Languages() {
		if len(fixturesFor(t, language)) == 0 {
			t.Errorf("testdata/%s/ has no sample; add one realistic file", language)
		}
	}
	for _, item := range loadFixtures(t) {
		if !Has(item.language) {
			t.Errorf("%s sits under testdata/%s/, which is not a language", item.path, item.language)
		}
	}
}

// Every sample, read as every language, keeps the contract: a tokenizer meets
// the other languages' code (a css block inside html, a pasted log) all the time.
func TestSamplesKeepTheContractInEveryLanguage(t *testing.T) {
	for _, item := range loadFixtures(t) {
		for _, language := range Languages() {
			tokens := tokenizeWith(registry[language], item.source)
			if problem := contractViolation(item.source, tokens); problem != "" {
				t.Errorf("%s read as %s: %s", item.path, language, problem)
			}
		}
	}
}

// Openers with no closer, escapes at the end, bytes that are not text: alone,
// three times over, and each one followed by each other one.
func TestHostileInputsKeepTheContractInEveryLanguage(t *testing.T) {
	var inputs []string
	for _, first := range hostileFragments {
		inputs = append(inputs, first, strings.Repeat(first, 3))
		for _, second := range hostileFragments {
			inputs = append(inputs, first+second)
		}
	}
	for _, language := range Languages() {
		tokenize := registry[language]
		for _, input := range inputs {
			if problem := contractViolation(input, tokenizeWith(tokenize, input)); problem != "" {
				t.Errorf("%s on %q: %s", language, input, problem)
			}
		}
	}
}

// Ten megabytes of samples and hostile fragments, mixed in a fixed order.
func TestTenMegabytesKeepTheContractInEveryLanguage(t *testing.T) {
	if testing.Short() {
		t.Skip("ten megabytes per language; skipped with -short")
	}
	source := mixedSource(t, 10<<20)
	for _, language := range Languages() {
		if problem := contractViolation(source, tokenizeWith(registry[language], source)); problem != "" {
			t.Errorf("%s on ten megabytes: %s", language, problem)
		}
	}
}

// mixedSource interleaves every sample and hostile fragment, chosen by a fixed
// linear congruential sequence, up to size bytes.
func mixedSource(t testing.TB, size int) string {
	pieces := append([]string(nil), hostileFragments...)
	for _, item := range loadFixtures(t) {
		pieces = append(pieces, item.source)
	}
	var builder strings.Builder
	builder.Grow(size + 64<<10)
	state := uint32(2463534242)
	for builder.Len() < size {
		state = state*1664525 + 1013904223
		builder.WriteString(pieces[int(state>>8)%len(pieces)])
	}
	return builder.String()
}

// A tokenizer is linear: sixteen times the input costs about sixteen times the
// time, never the 256 times a scan-ahead-per-byte tokenizer costs. Each hostile
// fragment and each language's own sample is repeated to 32 KiB and timed once.
// Under two milliseconds (16 MB/s) nothing more is asked: a quadratic tokenizer
// cannot finish 32 KiB of one opener that fast, and timer noise on fast runs
// cannot fail the test. Over it, 2 KiB and 32 KiB are timed in turns and the
// ratio of their best times judged (linearRatio).
//
// A busy machine (a fuzzer on every core, other packages' tests) can stall one
// stretch of runs and push a linear tokenizer past the limit, so a fragment over
// it is measured again after a pause, up to three times in all, and fails only
// when every measurement is over. A quadratic tokenizer is over it every time,
// by four times or more. A run that never returns fails after twenty seconds
// instead of hanging the suite.
func TestTokenizersRunInLinearTime(t *testing.T) {
	if testing.Short() {
		t.Skip("timing; skipped with -short")
	}
	const small, large, limit, attempts = 2 << 10, 32 << 10, 64, 3
	for _, language := range Languages() {
		tokenize := registry[language]
		fragments := append([]string(nil), hostileFragments...)
		for _, item := range fixturesFor(t, language) {
			fragments = append(fragments, item.source)
		}
		for _, fragment := range fragments {
			smallInput, largeInput := repeatTo(fragment, small), repeatTo(fragment, large)
			verdict := make(chan string, 1)
			go func() {
				started := time.Now()
				tokenizeWith(tokenize, largeInput)
				if time.Since(started) < 2*time.Millisecond {
					verdict <- ""
					return
				}
				var measurements []string
				for attempt := 0; attempt < attempts; attempt++ {
					if attempt > 0 {
						time.Sleep(50 * time.Millisecond) // let a burst of load pass
					}
					ratio, smallTime, largeTime := linearRatio(tokenize, smallInput, largeInput)
					if ratio <= limit {
						verdict <- ""
						return
					}
					measurements = append(measurements, fmt.Sprintf("%v and %v, %.0f times", smallTime, largeTime, ratio))
				}
				verdict <- strings.Join(measurements, "; ")
			}()
			select {
			case measured := <-verdict:
				if measured != "" {
					t.Errorf("%s is not linear on %q repeated: %d and %d bytes took %s longer for 16 times the input (every one of %d measurements over %d times)",
						language, clip(fragment), len(smallInput), len(largeInput), measured, attempts, limit)
				}
			case <-time.After(20 * time.Second):
				t.Fatalf("%s did not finish %q repeated to %d bytes in twenty seconds", language, clip(fragment), large)
			}
		}
	}
}

// linearRatio times small and large inputs in turns, five rounds after a
// collection, and returns the best large time over the best small time with
// both. Taking turns means a stretch of load slows both sizes, not one; the
// collection keeps one size from paying for the other's garbage.
func linearRatio(tokenize tokenizer, small, large string) (float64, time.Duration, time.Duration) {
	runtime.GC()
	smallTime, largeTime := time.Duration(1<<63-1), time.Duration(1<<63-1)
	for round := 0; round < 5; round++ {
		smallTime = min(smallTime, timedRun(tokenize, small))
		largeTime = min(largeTime, timedRun(tokenize, large))
	}
	return float64(largeTime) / float64(max(smallTime, time.Microsecond)), smallTime, largeTime
}

// timedRun is the time of one tokenization, repeated until it has run for at
// least a fifth of a millisecond, per run.
func timedRun(tokenize tokenizer, source string) time.Duration {
	runs, started := 0, time.Now()
	for runs == 0 || time.Since(started) < 200*time.Microsecond {
		tokenizeWith(tokenize, source)
		runs++
	}
	return time.Since(started) / time.Duration(runs)
}

// The harness has to fail a wrong table, or every language test passes vacuously.
func TestHarnessFailsWrongTables(t *testing.T) {
	expectFailures := func(name string, failures int, check func(record *recorder), mention string) {
		t.Helper()
		record := &recorder{}
		check(record)
		if len(record.failures) != failures {
			t.Errorf("%s: %d failures, want %d: %q", name, len(record.failures), failures, record.failures)
			return
		}
		if failures > 0 && !strings.Contains(record.failures[0], mention) {
			t.Errorf("%s: failure does not say %q: %s", name, mention, record.failures[0])
		}
	}
	expectFailures("a right row", 0, func(record *recorder) {
		checkExact(record, "text", []tokenCase{{"a b", tokensOf(Plain, "a b")}, {"", nil}})
	}, "")
	expectFailures("a row that does not join back into its source", 1, func(record *recorder) {
		checkExact(record, "text", []tokenCase{{"ab", tokensOf(Plain, "a")}})
	}, "the table row itself")
	expectFailures("a row with two plain neighbours", 1, func(record *recorder) {
		checkExact(record, "text", []tokenCase{{"ab", tokensOf(Plain, "a", Plain, "b")}})
	}, "the table row itself")
	expectFailures("a row the tokenizer disagrees with", 1, func(record *recorder) {
		checkExact(record, "text", []tokenCase{{"ab", tokensOf(Keyword, "ab")}})
	}, "wrong tokens from token 0")
	expectFailures("a language nobody registered", 1, func(record *recorder) {
		checkExact(record, "cobol", []tokenCase{{"a", tokensOf(Plain, "a")}})
	}, "no tokenizer")
	expectFailures("a contained whole token", 0, func(record *recorder) {
		checkContains(record, "text", []tokenCase{{"a b", tokensOf(Plain, "a b")}})
	}, "")
	expectFailures("a contained part of a token", 1, func(record *recorder) {
		checkContains(record, "text", []tokenCase{{"a b", tokensOf(Plain, "a")}})
	}, `no plain token "a"`)
}

// contractViolation is the judge for everything above: each way to break the
// contract must be named, and a list that keeps it must pass.
func TestContractViolationNamesEachBreak(t *testing.T) {
	for _, row := range []struct {
		source  string
		tokens  []Token
		mention string
	}{
		{"if x", tokensOf(Keyword, "if", Plain, " x"), ""},
		{"", nil, ""},
		{"\xff\xfe", tokensOf(Keyword, "\xff", String, "\xfe"), ""},
		{"ab", tokensOf(Plain, "a"), "do not join back"},
		{"ab", tokensOf(Plain, "b", Keyword, "a"), "do not join back"},
		{"ab", []Token{{Plain, "a"}, {Keyword, ""}, {String, "b"}}, "empty"},
		{"ab", tokensOf(Plain, "a", Plain, "b"), "merged"},
		{"a b", tokensOf(Plain, "a", Keyword, " ", String, "b"), "whitespace is plain"},
		{"$ ls", tokensOf(Prompt, "$", Plain, " ls"), "prompt"},
		{"x", []Token{{Kind(200), "x"}}, "outside the set"},
		{"é", tokensOf(Keyword, "\xc3", Plain, "\xa9"), "cuts a character"},
	} {
		got := contractViolation(row.source, row.tokens)
		switch {
		case row.mention == "" && got != "":
			t.Errorf("%q: a list that keeps the contract was failed: %s", row.source, got)
		case row.mention != "" && !strings.Contains(got, row.mention):
			t.Errorf("%q: want a violation naming %q, got %q", row.source, row.mention, got)
		}
	}
}
