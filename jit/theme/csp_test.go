package theme

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

const themedPage = `<html><head><script data-kitwork-jit="theme">x</script></head><body></body></html>`
const plainPage = `<html><head></head><body></body></html>`

// The hash must be the hash of the script the engine actually injects. Computed here from Render's
// own output rather than from the constant, so a change to either side that does not change the
// other is caught.
func TestPrepaintHashMatchesTheInjectedScript(t *testing.T) {
	out := Force(`<html><head></head><body></body></html>`)
	start := strings.Index(out, `<script data-kitwork-jit="theme">`)
	if start < 0 {
		t.Fatal("Force did not inject the pre-paint")
	}
	start += len(`<script data-kitwork-jit="theme">`)
	end := strings.Index(out[start:], "</script>")
	if end < 0 {
		t.Fatal("unterminated pre-paint script")
	}
	sum := sha256.Sum256([]byte(out[start : start+end]))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if PrepaintHash() != want {
		t.Fatalf("PrepaintHash() = %s, want %s — the hash must describe the script that ships", PrepaintHash(), want)
	}
}

// The policy the engine sends back: the hash is added where the script would be blocked, and
// nowhere else.
func TestAllowInCSPAddsTheHashOnlyWhereItIsNeeded(t *testing.T) {
	hash := PrepaintHash()
	for _, test := range []struct {
		name   string
		policy string
		html   string
		want   string
	}{
		{
			name:   "script-src that blocks it gains the hash",
			policy: "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'",
			html:   themedPage,
			want:   "default-src 'self'; script-src 'self' " + hash + "; style-src 'self' 'unsafe-inline'",
		},
		{
			name:   "a page without the pre-paint is untouched",
			policy: "script-src 'self'",
			html:   plainPage,
			want:   "script-src 'self'",
		},
		{
			name:   "no policy, nothing invented",
			policy: "",
			html:   themedPage,
			want:   "",
		},
		{
			// A hash switches 'unsafe-inline' OFF in browsers that understand it, so adding one
			// here would break every other inline script the site allowed on purpose.
			name:   "a policy that already allows inline scripts is left alone",
			policy: "script-src 'self' 'unsafe-inline'",
			html:   themedPage,
			want:   "script-src 'self' 'unsafe-inline'",
		},
		{
			name:   "already carrying the hash is left alone",
			policy: "script-src 'self' " + hash,
			html:   themedPage,
			want:   "script-src 'self' " + hash,
		},
		{
			// Scripts governed only by default-src get their own script-src, built from what
			// default-src already allows, so no other fetch type is widened.
			name:   "default-src only gains a script-src of its own",
			policy: "default-src 'self' https:",
			html:   themedPage,
			want:   "default-src 'self' https:; script-src 'self' https: " + hash,
		},
		{
			name:   "a policy about other things entirely is untouched",
			policy: "img-src 'self' data:",
			html:   themedPage,
			want:   "img-src 'self' data:",
		},
	} {
		if got := AllowInCSP(test.policy, test.html); got != test.want {
			t.Errorf("%s:\n  got  %q\n  want %q", test.name, got, test.want)
		}
	}
}

// Running twice must not append the hash twice: a cached response passes through this on every hit.
func TestAllowInCSPIsIdempotent(t *testing.T) {
	once := AllowInCSP("script-src 'self'", themedPage)
	twice := AllowInCSP(once, themedPage)
	if once != twice {
		t.Fatalf("second pass changed the policy:\n  once  %q\n  twice %q", once, twice)
	}
	if strings.Count(twice, "sha256-") != 1 {
		t.Fatalf("the hash must appear once, got %q", twice)
	}
}
