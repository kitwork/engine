package css

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Class-coverage audit. Skipped unless AUDIT_ROOT points at a site folder; it resolves every class
// token that site's markup actually uses and lists the ones that produce NO css at all — the silent
// failure mode where a class is written, looks like Tailwind, and does nothing. AUDIT_COLORS carries
// that site's own tokens ("brand=#0f766e,ink=#17211d,…") so a miss means a real gap and not merely a
// token this harness did not know about. This is how the v3 utility gap was found:
//
//	AUDIT_ROOT=/path/to/apps/<identity>/<domain> AUDIT_COLORS="brand=#0f766e,…" go test -run TestSiteAudit -v
func TestSiteAudit(t *testing.T) {
	root := os.Getenv("AUDIT_ROOT")
	if root == "" {
		t.Skip("set AUDIT_ROOT")
	}
	cfg := DefaultConfig
	cfg.Colors = map[string]Color{}
	for k, v := range DefaultConfig.Colors {
		cfg.Colors[k] = v
	}
	for _, kv := range strings.Split(os.Getenv("AUDIT_COLORS"), ",") {
		if p := strings.SplitN(kv, "=", 2); len(p) == 2 {
			cfg.Colors[p[0]] = Hex(p[1])
		}
	}

	classRe := regexp.MustCompile(`class="([^"]*)"`)
	seen := map[string]int{}
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".html") && !strings.HasSuffix(p, ".js") {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, m := range classRe.FindAllStringSubmatch(string(b), -1) {
			for _, c := range strings.Fields(m[1]) {
				if c == "" || strings.ContainsAny(c, "{}") {
					continue
				}
				seen[c]++
			}
		}
		return nil
	})

	type miss struct {
		cls string
		n   int
	}
	var dead []miss
	total, uses := 0, 0
	for c, n := range seen {
		total++
		uses += n
		if strings.HasPrefix(c, "icon-") || strings.HasPrefix(c, "logo-") {
			continue
		}
		css, _, _ := ResolveCore(c, &cfg)
		if strings.TrimSpace(css) == "" {
			dead = append(dead, miss{c, n})
		}
	}
	sort.Slice(dead, func(i, j int) bool { return dead[i].n > dead[j].n })
	deadUses := 0
	for _, d := range dead {
		deadUses += d.n
	}
	t.Logf("%s: %d distinct classes / %d uses; UNRESOLVED %d distinct / %d uses", filepath.Base(root), total, uses, len(dead), deadUses)
	for i, d := range dead {
		if i >= 500 {
			t.Logf("  … and %d more", len(dead)-500)
			break
		}
		t.Logf("  %-40s x%d", d.cls, d.n)
	}
}
