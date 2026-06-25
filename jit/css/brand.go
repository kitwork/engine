package css

import "strings"

// Brand-logo colours as first-class jitcss colour tokens. A `brand-<slug>` colour name resolves to
// a third-party brand's official hex — so the whole colour machinery works for it for free:
// `text-brand-github`, `bg-brand-stripe`, `border-brand-discord`, `ring-brand-vercel`,
// gradient stops (`from-brand-github`), alpha (`text-brand-github/50`) and every variant
// (`hover:text-brand-github`, `dark:bg-brand-x`). This stays consistent with the existing house
// colour `brand` (= Kitwork red): bare `brand` is us, `brand-<who>` is that brand.
//
// The hex data lives in jit/logo (the brandColor map). To avoid jit/css depending on jit/logo, the
// source is injected: jit/logo calls RegisterBrandPalette at init. Unwired, `brand-*` simply does
// not resolve and the colour utility falls through (the class is dropped, like any unknown colour).

// brandResolver maps a brand slug → its official hex ("#181717", true). nil until registered.
var brandResolver func(slug string) (string, bool)

// RegisterBrandPalette wires a brand-colour source (jit/logo) into the colour resolver. Called once
// at init; later calls replace it (tests use this to install a stub).
func RegisterBrandPalette(fn func(slug string) (string, bool)) { brandResolver = fn }

// brandHex resolves a `brand-<slug>` colour token to its hex, or ("", false) when unwired, when the
// name is not brand-prefixed, or when the slug is unknown. Consulted inside twColor.
func brandHex(colorName string) (string, bool) {
	if brandResolver == nil || !strings.HasPrefix(colorName, "brand-") {
		return "", false
	}
	return brandResolver(colorName[len("brand-"):])
}
