package work

import (
	"net/http"

	theme "github.com/kitwork/engine/jit/theme"
)

// allowThemePrepaint lets the engine's own anti-flash script run under the site's own policy.
//
// The pre-paint must be inline and synchronous or it cannot beat the first frame; a site that sends
// `script-src 'self'` blocks it and flashes the wrong palette on every load. The engine put that
// script there, so the engine declares it — by hash, which permits exactly that one script. It adds
// nothing when the page has no pre-paint, when the site sent no policy, or when the policy already
// allows inline scripts (adding a hash there would switch 'unsafe-inline' off and break the site's
// own scripts).
func allowThemePrepaint(header http.Header, html []byte) {
	policy := header.Get("Content-Security-Policy")
	if policy == "" {
		return
	}
	if updated := theme.AllowInCSP(policy, string(html)); updated != policy {
		header.Set("Content-Security-Policy", updated)
	}
}
