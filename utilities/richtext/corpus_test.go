package richtext

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// corpusCase is one case of a file under testdata: a source and the HTML Clean must give.
type corpusCase struct {
	file, name   string
	source, want string
	hasWant      bool
}

// loadCorpus reads testdata/<file>. A case is "== name", the source lines, "-- want" and the expected
// lines; lines starting with "#" before the first case are comments.
func loadCorpus(t testing.TB, file string) []corpusCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	var cases []corpusCase
	var source, want []string
	inWant := false
	flush := func() {
		if len(cases) == 0 {
			return
		}
		c := &cases[len(cases)-1]
		c.source = expandCorpus(strings.Join(trimBlankLines(source), "\n"))
		c.want = expandCorpus(strings.Join(trimBlankLines(want), "\n"))
		c.hasWant = inWant
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "== "):
			flush()
			cases = append(cases, corpusCase{file: file, name: strings.TrimPrefix(line, "== ")})
			source, want, inWant = nil, nil, false
		case len(cases) == 0:
			if line != "" && !strings.HasPrefix(line, "#") {
				t.Fatalf("%s: text before the first case: %q", file, line)
			}
		case line == "-- want" && !inWant:
			inWant = true
		case inWant:
			want = append(want, line)
		default:
			source = append(source, line)
		}
	}
	flush()
	if len(cases) == 0 {
		t.Fatalf("%s holds no case", file)
	}
	return cases
}

func trimBlankLines(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

var corpusNames = map[string]string{
	"NUL": "\x00", "SOH": "\x01", "TAB": "\t", "LF": "\n", "CR": "\r", "FF": "\f", "VT": "\v",
	"ESC": "\x1b", "DEL": "\x7f", "NBSP": "\u00a0", "ZWSP": "\u200b", "BOM": "\ufeff",
}

var corpusEscape = regexp.MustCompile(`\{([A-Z]+|U\+[0-9A-F]{4,6}|BYTE:[0-9A-F]{2})\}`)

// expandCorpus turns {NUL}, {U+2028}, {BYTE:FF}… into the characters (or bytes) they stand for.
func expandCorpus(s string) string {
	return corpusEscape.ReplaceAllStringFunc(s, func(m string) string {
		name := m[1 : len(m)-1]
		switch {
		case strings.HasPrefix(name, "U+"):
			n, _ := strconv.ParseUint(name[2:], 16, 32)
			return string(rune(n))
		case strings.HasPrefix(name, "BYTE:"):
			n, _ := strconv.ParseUint(name[5:], 16, 8)
			return string([]byte{byte(n)})
		}
		if c, ok := corpusNames[name]; ok {
			return c
		}
		return m
	})
}

// corpus is every case of every corpus file.
func corpus(t testing.TB) []corpusCase {
	return append(loadCorpus(t, "xss.txt"), loadCorpus(t, "paste.txt")...)
}

// The corpus: filter-evasion payloads in the style of the OWASP cheat sheet, mutation XSS from
// DOMPurify and PortSwigger research, references without ";", controls and backslashes in addresses,
// and for the grammar of 20/10/2026 alignment classes injected or spelt with references, text-align
// with expression() and url(), markup and end tags breaking out of a pre, code inside a and a inside
// code, rules with attributes (testdata/xss.txt, 141 cases); and the clipboard of Google Docs, Word, a
// browser, a spreadsheet, editors, a rendered markdown page and a code editor (testdata/paste.txt, 15).
// Each case gives exactly the HTML its file says, and holds every invariant the fuzz test checks: the
// grammar, href and the two alignment classes the only attributes, allowed addresses only, no raw "&"
// in an href, Clean(Clean(x)) == Clean(x), and the output read again gives the same tree.
//
// CONTROL: with NormalizeAddress no longer taking C0 controls out (its c < 0x20 case removed), the
// case "tab between the slashes" kept <a href="/	/evil.example"> and this test failed.
//
// CONTROL: with a class matched by its prefix (strings.HasPrefix in explicitAlignment in place of the
// exact names), the case "a class breaking out of its quotes" (its token align-center") came out
// aligned and this test failed on it.
func TestCorpus(t *testing.T) {
	for _, c := range corpus(t) {
		t.Run(c.file+"/"+c.name, func(t *testing.T) {
			if !c.hasWant {
				t.Fatalf("the case has no want")
			}
			if got := Clean(c.source).HTML; got != c.want {
				t.Errorf("Clean(%q)\n got  %q\n want %q", c.source, got, c.want)
			}
			checkClean(t, c.source)
		})
	}
}
