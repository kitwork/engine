package theme

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"sync"
)

// The pre-paint has to be INLINE and synchronous — a deferred file cannot stop the first frame from
// painting the wrong palette. A site with a Content-Security-Policy that says `script-src 'self'`
// therefore blocks the one script whose whole job is to prevent a flash, and the visitor sees the
// flash on every load. That is not a policy the site chose: it is the engine's own script failing a
// rule the engine knows about.
//
// So the engine declares it. The pre-paint body is a CONSTANT, so its hash is a constant too, and a
// hash is exactly what CSP offers for this: permission for ONE known script, nothing wider. The
// engine adds `'sha256-…'` to the policy it is about to send, and never adds anything else.
//
// Two refusals matter as much as the addition:
//
//   - if the policy already allows 'unsafe-inline', the hash is NOT added. In CSP a hash or nonce
//     turns 'unsafe-inline' OFF for browsers that understand it, so adding one here would break
//     every other inline script the site deliberately allowed.
//   - if the site declared no policy at all, nothing is written. The engine does not invent a
//     security header a site did not ask for.
var (
	prepaintHashOnce  sync.Once
	prepaintHashValue string
)

// PrepaintHash is the CSP source expression for the inline pre-paint this package injects, e.g.
// `'sha256-7DTo+ePXU5XBhJeEF7glQskJ3hjmKB0WvugP+YkEqzg='`. A site that builds its policy by hand
// can read it instead of copying a digest that would go stale the moment the script changes.
func PrepaintHash() string {
	prepaintHashOnce.Do(func() {
		sum := sha256.Sum256([]byte(prepaintBody))
		prepaintHashValue = "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	})
	return prepaintHashValue
}

// AllowInCSP returns the policy the response should carry so the pre-paint in this page can run.
// It is a no-op for a page without the pre-paint, for an empty policy, and for a policy that
// already allows the script — by hash or by 'unsafe-inline'.
func AllowInCSP(policy, html string) string {
	if policy == "" || !strings.Contains(html, `data-kitwork-jit="theme"`) {
		return policy
	}
	hash := PrepaintHash()
	if strings.Contains(policy, hash) {
		return policy
	}

	directives := strings.Split(policy, ";")
	scriptIndex, defaultIndex := -1, -1
	for index, directive := range directives {
		switch strings.ToLower(directiveName(directive)) {
		case "script-src":
			scriptIndex = index
		case "default-src":
			defaultIndex = index
		}
	}

	// A policy that governs scripts only through default-src gets its own script-src, built from
	// what default-src already allows plus this one hash. Narrowing to a new directive changes
	// nothing for the other fetch types.
	if scriptIndex < 0 {
		if defaultIndex < 0 {
			return policy
		}
		sources := strings.TrimSpace(directiveValue(directives[defaultIndex]))
		if strings.Contains(sources, "'unsafe-inline'") {
			return policy
		}
		added := "script-src " + sources + " " + hash
		return strings.TrimRight(strings.TrimSpace(policy), ";") + "; " + added
	}

	if strings.Contains(directives[scriptIndex], "'unsafe-inline'") {
		return policy
	}
	directives[scriptIndex] = strings.TrimRight(directives[scriptIndex], " ") + " " + hash
	return strings.Join(directives, ";")
}

// directiveName is the first token of a CSP directive, keeping any leading space so the caller can
// rebuild the policy exactly as the site wrote it.
func directiveName(directive string) string {
	fields := strings.Fields(directive)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// directiveValue is everything after the directive's name.
func directiveValue(directive string) string {
	fields := strings.Fields(directive)
	if len(fields) < 2 {
		return ""
	}
	return strings.Join(fields[1:], " ")
}
