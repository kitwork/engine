package theme

import "testing"

// component="appearance" — the light/dark toggle of a Kitwork UI page — is written data-kitwork-component=
// "appearance", with or without an alias or a version. It uses the theme system as a theme host does,
// so the page gets the pre-paint, and does not flash.
func TestRenderSeesTheAppearanceHost(t *testing.T) {
	for _, use := range []string{
		`<head></head><body><div data-kitwork-component="appearance"></div></body>`,
		`<head></head><body><div data-kitwork-component="appearance: $theme"></div></body>`,
		`<head></head><body><div data-kitwork-component=" appearance@1.0.0 : $theme "></div></body>`,
		`<head></head><body><div data-kitwork-component="theme: $theme"></div></body>`,
		`<head></head><body><main data-kitwork-component="app@1.1.0: $app"></main></body>`,
	} {
		if !injected(Render(use)) {
			t.Errorf("theme system not detected in: %s", use)
		}
	}
	// control: other components, and an alias that merely reads like one, bring no pre-paint
	for _, html := range []string{
		`<head></head><body><div data-kitwork-component="dropdown: $account"></div></body>`,
		`<head></head><body><div data-kitwork-component="menu: $appearance"></div></body>`,
		`<head></head><body><div data-kitwork-component="appearances"></div></body>`,
	} {
		if Render(html) != html {
			t.Errorf("not the theme system, yet pre-painted: %s", html)
		}
	}
}
