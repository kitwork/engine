package work

// A Kitwork UI site's routes take a request that may change something only from the site's own pages
// (20/10/2026). Every request a router.work route answers that is not GET, HEAD or OPTIONS goes through
// net/http's CrossOriginProtection before any limit, guard or handler: a browser's request from a page
// of another site is refused with 403. Another site is any other: a sibling under the same domain
// (shop.kitwork.io and other.kitwork.io are "same-site" to a browser) is refused as a stranger is. A
// request passes when the browser says it comes from the site's own pages (Sec-Fetch-Site:
// same-origin, or an Origin that is the Host), and when it carries neither header: no browser sent it
// (a webhook, a server calling the site). A template site (router.kitwork.js) is not checked: it keeps
// what it answered. No site can be let in yet: cors is a name config.work keeps, with no rule
// (KITWORK_ROUTER §14).

import (
	"fmt"
	"net/http"
)

// crossOriginProtection is the check, with no origin trusted and no path let through.
var crossOriginProtection = http.NewCrossOriginProtection()

// crossOriginRefusal is the error of a request crossOriginProtection refuses, nil for one it lets
// through. A visitor reads only "Forbidden" and the request id (writeFailure); while developing the
// text says what refused it.
func crossOriginRefusal(r *http.Request) error {
	if crossOriginProtection.Check(r) == nil {
		return nil
	}
	reason := "Sec-Fetch-Site: " + r.Header.Get("Sec-Fetch-Site")
	if r.Header.Get("Sec-Fetch-Site") == "" {
		reason = fmt.Sprintf("Origin %q không phải Host %q", r.Header.Get("Origin"), r.Host)
	}
	return uncaught(fmt.Sprintf("%s %s bị từ chối: request đến từ trang của một site khác (%s). Route trong router.work chỉ nhận %s từ trang của chính site này, hay từ máy gọi thẳng không qua trình duyệt (webhook); chưa có cách mở cho site khác",
		r.Method, r.URL.Path, reason, r.Method))
}
