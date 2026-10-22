package work

// The cookie's safe defaults and the rules router.work checks (KITWORK_ROUTER §6, 20/10/2026): Path=/,
// HttpOnly, SameSite=Lax, and Secure when the request came over https; choices path, domain, maxAge
// (seconds or a span, "30d"), expires, secure, httpOnly, sameSite ("lax", "strict", "none"). In
// router.work sameSite "none" without Secure is an error (the browser drops such a cookie), and a
// sameSite it does not know is an error, not a quiet Lax. A template site keeps its quiet Lax.

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

// overHTTPS reports whether the request of the response came over https (requestScheme).
func (r *Response) overHTTPS() bool {
	return r.ctx != nil && r.ctx.request != nil && requestScheme(r.ctx.request.request()) == "https"
}

// cookieExpires reads expires: a date, a time in milliseconds since 1970 (Date.now() + …), or a text
// date (RFC 3339 or HTTP's).
func cookieExpires(v value.Value) (time.Time, bool) {
	switch {
	case v.K == value.Time:
		return time.Unix(0, int64(v.N)).UTC(), true
	case v.IsNumber():
		return time.UnixMilli(int64(v.N)).UTC(), true
	case v.IsString():
		text := strings.TrimSpace(v.Text())
		for _, layout := range []string{time.RFC3339, time.RFC1123, http.TimeFormat, "2006-01-02"} {
			if at, err := time.Parse(layout, text); err == nil {
				return at.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

// checkCookieOptions is router.work's check of a cookie's choices before it is set.
func (r *Response) checkCookieOptions(name string, options []value.Value) error {
	secure := r.overHTTPS()
	sameSite := "lax"
	if len(options) > 0 && options[0].IsMap() {
		choices := options[0].Map()
		if v, ok := choices["secure"]; ok {
			secure = v.Truthy()
		}
		if v, ok := choices["sameSite"]; ok {
			sameSite = strings.ToLower(strings.TrimSpace(v.Text()))
			switch sameSite {
			case "lax", "strict", "none":
			default:
				return fmt.Errorf("cookie %q: sameSite %q không có: sameSite là \"lax\", \"strict\" hay \"none\"", name, v.Text())
			}
		}
		if v, ok := choices["expires"]; ok {
			if _, ok := cookieExpires(v); !ok {
				return fmt.Errorf("cookie %q: không đọc được expires %q: viết một ngày (\"2026-12-31\"), hay Date.now() + số mili giây", name, v.Text())
			}
		}
	}
	if sameSite == "none" && !secure {
		return fmt.Errorf("cookie %q: sameSite \"none\" cần secure: true, vì trình duyệt bỏ cookie đó khi thiếu Secure", name)
	}
	return nil
}
