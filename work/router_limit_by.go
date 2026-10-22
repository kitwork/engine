package work

// What a rate limit counts, by (KITWORK_ROUTER §4, 20/10/2026): "ip" (the default), "global" (one count
// for everyone), "user" (the user of a checked session). Nothing a client sends unchecked: a client
// that changes a header or a cookie it chose leaves the limit behind.
//
// router.work takes ip and global; user is a boot error until the engine has checked sessions; browser
// and every other count read from what the client sends are errors. A template site keeps what it
// counts, with one line per file at boot saying the client can change it.

import (
	"fmt"
	"time"

	"github.com/kitwork/engine/value"
)

// checkLimitBy checks one rate limit's by as the folder's router file is read.
func (f *FolderRouter) checkLimitBy(by string) {
	if f == nil {
		return
	}
	switch by {
	case "", "ip", "global":
		return
	}
	at := ""
	if f.callSite != nil {
		if line := f.callSite().Line; line > 0 {
			at = fmt.Sprintf("dòng %d: ", line)
		}
	}
	if !f.ui {
		if (by == "user" || by == "browser") && f.limitWarning == "" {
			f.limitWarning = fmt.Sprintf("%sgiới hạn tần suất by: %q đếm theo header hay cookie client tự gửi (Authorization, cookie session, __device_id, X-Device-ID), đổi chúng là thoát giới hạn: dùng by: \"ip\" hay \"global\"", at, by)
		}
		return
	}
	if f.limitErr != nil {
		return
	}
	switch by {
	case "user":
		f.limitErr = fmt.Errorf("%sgiới hạn tần suất by: \"user\" đếm theo người dùng của phiên đã kiểm, mà engine chưa có phiên đã kiểm: dùng by: \"ip\" hay \"global\"", at)
	case "browser":
		f.limitErr = fmt.Errorf("%sgiới hạn tần suất by: \"browser\" lấy từ header hay cookie client tự gửi, đổi chúng là thoát giới hạn: dùng by: \"ip\" hay \"global\"", at)
	default:
		f.limitErr = fmt.Errorf("%sgiới hạn tần suất by: %q không có: by là \"ip\" (mặc định), \"global\" hay \"user\"", at, by)
	}
}

// checkExpiry says what an expiry of .cache()/.persist() the engine cannot read does (KITWORK_ROUTER
// §9, 20/10/2026): in router.work an error at boot (the route's cache configuration error), in a template
// site one line per file. It no longer turns the cache off without a word.
func (m *FolderMethod) checkExpiry(name string, expiry any, args []value.Value) {
	if len(args) == 0 || !isNilExpiry(expiry) {
		return
	}
	message := fmt.Sprintf(".%s(%q): không đọc được hạn: viết một khoảng (\"5m\", \"1h\"), số mili giây, hay một mốc giờ (\"nextday 03:00\")", name, args[0].Text())
	if m.folder != nil && m.folder.ui {
		if m.cacheKeyErr == nil {
			m.cacheKeyErr = fmt.Errorf("%s", message)
		}
		return
	}
	if m.folder != nil && m.folder.expiryWarning == "" {
		m.folder.expiryWarning = message + "; route này không cache"
	}
}

// isNilExpiry reports whether an expiry resolver is nil: expiryOf found nothing to read.
func isNilExpiry(expiry any) bool {
	resolver, ok := expiry.(func(time.Time) time.Duration)
	return !ok || resolver == nil
}
