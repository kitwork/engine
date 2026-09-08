package core

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Design tokens are published as an "r, g, b" TRIPLET, not a colour: the colour
// utilities read them as rgb(var(--color-x, 248, 34, 68)) so an alpha suffix can
// composite through the same variable. Writing a colour there is the natural
// mistake, and CSS punishes it in the worst way available — rgb(#00ff00) is
// invalid, the whole declaration is dropped, and the property silently falls
// back to whatever it inherits. No error, no warning, just the wrong colour.
//
// CSS cannot report that. This pass can, at build time, pointing at the file and
// line, which is the only place the mistake is cheap to fix.
var colorTokenDeclarationRe = regexp.MustCompile(`--color-[a-z0-9-]+\s*:\s*([^;}\n]+)`)

// tripletRe is the accepted form. A var() reference is accepted too: aliasing one
// token to another substitutes a triplet and stays valid.
var tripletRe = regexp.MustCompile(`^\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}$`)

func validTokenValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "var(") {
		return true
	}
	// Built at runtime, not written down: a JS template literal or a Kitwork
	// template expression. What it evaluates to cannot be known here, and a check
	// that fires on correct code is worse than no check — people learn to ignore
	// it, and then it cannot report the real mistake either.
	if strings.Contains(value, "${") || strings.Contains(value, "{{") {
		return true
	}
	return tripletRe.MatchString(value)
}

// checkColorTokenFormat reports every design-token declaration in a site's
// templates whose value is not a triplet.
func checkColorTokenFormat(directory string) []CheckIssue {
	var issues []CheckIssue
	_ = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".kitwork.html") {
			return nil
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for line := 1; scanner.Scan(); line++ {
			text := scanner.Text()
			if !strings.Contains(text, "--color-") {
				continue
			}
			for _, match := range colorTokenDeclarationRe.FindAllStringSubmatch(text, -1) {
				value := strings.TrimSpace(match[1])
				if validTokenValue(value) {
					continue
				}
				issues = append(issues, CheckIssue{
					Stage: "design tokens",
					File:  fmt.Sprintf("%s:%d", path, line),
					Err: fmt.Errorf(
						"%q publishes %q; a design token is an \"r, g, b\" triplet, so this declaration is dropped by the browser and the colour silently falls back",
						strings.SplitN(strings.TrimSpace(match[0]), ":", 2)[0], value,
					),
				})
			}
		}
		return nil
	})
	return issues
}
