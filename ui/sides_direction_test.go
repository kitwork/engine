package ui

import (
	"strings"
	"testing"
)

// A page compiled for a site written right to left carries dir="rtl" on <html> (review 13/10/2026):
// the browser does not take the direction from lang, and left and right, which the stylesheet writes as
// logical CSS since 19/10/2026 (padding-left is padding-inline-start), mirror only under dir. The
// direction is the compile's, Site.Language (Page.rightToLeft): a request's own language (a view's ctx
// language) changes lang only. CONTROL: a site written left to right (none written, vi, en, a code that
// only starts like ar) writes no dir.
func TestRightToLeftSiteWritesItsDirection(t *testing.T) {
	source := content(`<content><box padding-left="4"><text>x</text></box></content>`)
	for _, c := range []struct{ site, request, want string }{
		{"ar", "ar", `<html lang="ar" dir="rtl">`},
		{"he_IL", "he-IL", `<html lang="he-IL" dir="rtl">`},
		{"fa-IR", "fa-IR", `<html lang="fa-IR" dir="rtl">`},
		{"UR", "ur", `<html lang="ur" dir="rtl">`},
		{"ar", "vi", `<html lang="vi" dir="rtl">`}, // the compile's direction, the request's lang
		// CONTROL
		{"", "", `<html lang="vi">`},
		{"vi", "vi", `<html lang="vi">`},
		{"en", "en", `<html lang="en">`},
		{"arn", "arn", `<html lang="arn">`},
		{"vi", "ar", `<html lang="ar">`}, // a request's language alone: the page compiled left to right
	} {
		s := memorySite("", source)
		s.Reload = true
		s.Language = c.site
		page, err := s.Compile("/site")
		if err != nil {
			t.Fatalf("%s: %v", c.site, err)
		}
		document := page.Document(Request{Path: "/", Language: c.request})
		if !strings.Contains(document, c.want+"\n") {
			t.Errorf("site %q, request %q:\n got  %.80s\n want %s", c.site, c.request, document[strings.Index(document, "<html"):], c.want)
		}
	}
}
