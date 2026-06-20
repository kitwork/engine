package domain

import "testing"

func TestTarget(t *testing.T) {
	Configure("apex", map[string]string{
		"old.com":   "new.com",
		"alias.com": "https://main.com",
	})

	cases := []struct {
		name       string
		scheme     string
		host       string
		path       string
		query      string
		forceHTTPS bool
		want       string
		wantOK     bool
	}{
		{"www→apex", "https", "www.kitwork.io", "/x", "", false, "https://kitwork.io/x", true},
		{"apex already canonical", "https", "kitwork.io", "/x", "", false, "", false},
		{"domain map + keep path/query", "https", "old.com", "/p", "q=1", false, "https://new.com/p?q=1", true},
		{"domain map full URL", "https", "alias.com", "/p", "", false, "https://main.com/p", true},
		{"http→https", "http", "kitwork.io", "/x", "", true, "https://kitwork.io/x", true},
		{"combined: http+www+map", "http", "www.old.com", "/x", "", true, "https://old.com/x", true},
		{"localhost untouched", "https", "localhost", "/", "", false, "", false},
		{"raw IP untouched", "https", "10.0.0.5", "/", "", false, "", false},
	}
	for _, c := range cases {
		got, ok := Target(c.scheme, c.host, c.path, c.query, c.forceHTTPS)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: Target(%s,%s)=%q,%v want %q,%v", c.name, c.scheme, c.host, got, ok, c.want, c.wantOK)
		}
	}
}

// A canonical rule that re-adds what a map strips must NOT loop: single-shot
// resolution returns the original (no redirect) instead of bouncing forever.
func TestTargetNoLoop(t *testing.T) {
	Configure("www", map[string]string{"www.x.com": "x.com"})
	if got, ok := Target("https", "www.x.com", "/", "", false); ok {
		t.Errorf("expected no redirect (loop guard), got %q", got)
	}
}

func TestTargetApexToWww(t *testing.T) {
	Configure("www", nil)
	if got, ok := Target("https", "kitwork.io", "/", "", false); !ok || got != "https://www.kitwork.io/" {
		t.Errorf("apex→www: got %q,%v", got, ok)
	}
}

func TestRedirectURL(t *testing.T) {
	if got := RedirectURL("https", "new.com", "/p", "q=1"); got != "https://new.com/p?q=1" {
		t.Errorf("host target: %q", got)
	}
	if got := RedirectURL("https", "https://full.com", "/p", ""); got != "https://full.com/p" {
		t.Errorf("full-url target: %q", got)
	}
}

// With no system DB configured, the per-domain redirect lookup must return "" and
// must not panic (fail-open). Also exercises the cache path.
func TestDBRedirectTargetNoDB(t *testing.T) {
	if got := DBRedirectTarget("anything.com"); got != "" {
		t.Errorf("no system DB → expected \"\", got %q", got)
	}
	if got := DBRedirectTarget("anything.com"); got != "" { // cached
		t.Errorf("cached: %q", got)
	}
}
