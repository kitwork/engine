package value

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// failuresWithoutACode is how many places of the engine still make a failure without a code of the
// set, which error.code reads as ERROR (14/10/2026): a ratchet. It may only go down, to zero, when
// ERROR has no reason left to exist. Today they are the fallbacks for what tells nothing: a Go error a
// host function returned (FailureOfError), a raw failure without a payload (FailureFrom, twice, and
// runtime/safe.go, twice), and an answer the engine could not write that ends a request
// (work/router.go errorObject). What it cannot see: FailureOfError counts once for every Go method a
// program reaches that returns a bare error, so such a method must name its code itself, with a
// FailureCode() string method on its error (work.codedError; napas was one, 14/10/2026).
const failuresWithoutACode = 7

var (
	// a failure built by hand: value.Value{K: value.Invalid, V: …}, Value{K: Invalid…}
	invalidLiteral = regexp.MustCompile(`K:\s*(value\.)?Invalid\b`)
	// a failure given the fallback code on purpose
	fallbackCode = regexp.MustCompile(`(InvalidFailure|NewFailure)\((value\.)?DefaultFailureCode|(:=|[^!=<>]=)\s*value\.DefaultFailureCode\b`)
)

// constructors are the two places that build a failure for everyone else: InvalidFailure, and the
// runtime's diagnostic of a run.
var constructors = map[string]bool{
	filepath.Join("value", "failure.go"):      true,
	filepath.Join("runtime", "diagnostic.go"): true,
}

func TestFailuresWithoutACodeOnlyGoDown(t *testing.T) {
	// the engine's tree; KITWORK_FAILURE_RATCHET_ROOT points the count at a copy of it instead (the proof
	// that the count goes red when a place without a code is added or one is taken away)
	root := os.Getenv("KITWORK_FAILURE_RATCHET_ROOT")
	if root == "" {
		root = ".."
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	var places []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".artifacts", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
		for line := 1; scanner.Scan(); line++ {
			text := scanner.Text()
			if strings.HasPrefix(strings.TrimSpace(text), "//") {
				continue
			}
			if (invalidLiteral.MatchString(text) && !constructors[relative]) || fallbackCode.MatchString(text) {
				places = append(places, relative+":"+itoa(line)+": "+strings.TrimSpace(text))
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case len(places) > failuresWithoutACode:
		t.Fatalf("%d places make a failure without a code, the ratchet allows %d: give the new one a code of the set (value.InvalidFailure)\n%s",
			len(places), failuresWithoutACode, strings.Join(places, "\n"))
	case len(places) < failuresWithoutACode:
		t.Fatalf("%d places make a failure without a code: lower failuresWithoutACode to %d\n%s",
			len(places), len(places), strings.Join(places, "\n"))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}
