package publishing_test

import (
	"strings"
	"testing"

	"github.com/kitwork/engine/utilities/publishing"
	"github.com/kitwork/engine/value"
)

func strs(items ...string) value.Value {
	vals := make([]value.Value, len(items))
	for i, s := range items {
		vals[i] = value.New(s)
	}
	return value.New(vals)
}

// router.robots() with no config → allow-all plus a Sitemap line for the serving domain.
func TestRobotsZeroConfig(t *testing.T) {
	got := publishing.Robots(value.New(map[string]value.Value{}), "https://x.com")
	for _, want := range []string{"User-agent: *", "Allow: /", "Sitemap: https://x.com/sitemap.xml"} {
		if !strings.Contains(got, want) {
			t.Fatalf("zero-config robots missing %q:\n%s", want, got)
		}
	}
	// An absent field must not print a line — reading a missing key must not leak "invalid".
	if strings.Contains(got, "Host:") || strings.Contains(got, "invalid") {
		t.Fatalf("zero-config robots leaked a spurious line:\n%s", got)
	}
}

func TestRobotsDisallowAndSitemapControl(t *testing.T) {
	got := publishing.Robots(value.New(map[string]value.Value{
		"disallow": strs("/dashboard", "/api"),
	}), "https://x.com")
	for _, want := range []string{"Disallow: /dashboard", "Disallow: /api", "Sitemap: https://x.com/sitemap.xml"} {
		if !strings.Contains(got, want) {
			t.Fatalf("robots missing %q:\n%s", want, got)
		}
	}

	// sitemap: false omits the Sitemap line entirely.
	off := publishing.Robots(value.New(map[string]value.Value{"sitemap": value.New(false)}), "https://x.com")
	if strings.Contains(off, "Sitemap:") {
		t.Fatalf("sitemap:false must omit the Sitemap line:\n%s", off)
	}

	// A custom sitemap URL is honored verbatim.
	custom := publishing.Robots(value.New(map[string]value.Value{"sitemap": value.New("https://cdn.x.com/sm.xml")}), "https://x.com")
	if !strings.Contains(custom, "Sitemap: https://cdn.x.com/sm.xml") {
		t.Fatalf("custom sitemap url not honored:\n%s", custom)
	}
}
