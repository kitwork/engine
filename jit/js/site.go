package js

// A site's OWN components: `_components/<name>.js` beside the site, or shared by every domain of
// one identity one level up — the same walk-up `_core` imports use. The file registers the
// component exactly as an embedded one does:
//
//	// apps/<identity>/<domain>/_components/bid-stepper.js
//	kit.component("bid-stepper", { amount: 0, init(context) { … } });
//
// and the markup names it with no version:
//
//	<div data-kit-component="bid-stepper">
//
// There is no version, on purpose. The embedded catalogue is versioned because the engine ships it
// and must not change under a site that pinned a spelling; a site's own component ships WITH the
// site, so its file IS the contract — change the contract and change the name. That also keeps the
// runtime honest: `name@1.2.0` can only ever mean an engine component.
//
// Only pages that name one get it, through the same one cacheable /kit.js?components= request.
type Site interface {
	// Component returns the module source for a tenant component, or "" when the site has none by
	// that name. Implementations are read at generation time and frozen, so a request never reads
	// the filesystem.
	Component(name string) string
}

// siteComponent resolves a tenant component, refusing anything that carries a version (only the
// embedded catalogue has versions) and anything the site does not own.
func siteComponent(site Site, nameWithVersion string) string {
	if site == nil || nameWithVersion == "" || nameWithVersion == coreName {
		return ""
	}
	if !componentNameRe.MatchString(nameWithVersion) {
		return "" // a version suffix (or any other shape) is never a tenant component
	}
	return site.Component(nameWithVersion)
}

// hasComponent is HasComponent plus the tenant's own set. The embedded catalogue wins: a site
// cannot shadow `dialog` and change what that name means for a reader.
func hasComponent(nameWithVersion string, site Site) bool {
	return HasComponent(nameWithVersion) || siteComponent(site, nameWithVersion) != ""
}

// componentSource is the module text for one component key's name part.
func componentSource(nameWithVersion string, site Site) string {
	if s := readComponent(nameWithVersion); s != "" {
		return s
	}
	return siteComponent(site, nameWithVersion)
}
