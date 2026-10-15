package js

import (
	"regexp"
	"strings"

	hydrate "github.com/kitwork/engine/jit/hydrate"
	"github.com/kitwork/engine/jit/internal/htmlattr"
)

// Directives whose machinery stays out of the kernel. The kernel knows the attribute's name and runs
// its expression through the event pipeline; the part that watches the browser is a file of its own,
// delivered only to a page that writes the directive — like a component, but named by the grammar.
//
//	data-kit-intersect[:leave][:once][:half|:full][:margin(x)]  → directives/intersect.js
//	data-kit-drag="true" | "false"                              → directives/drag.js
var directiveMarkers = map[string]string{"intersect": "data-kit-intersect", "drag": "data-kit-drag"}

func directiveSource(name string) string {
	if _, known := directiveMarkers[name]; !known {
		return ""
	}
	key := "directive:" + name
	if v, ok := moduleCache.Load(key); ok {
		return v.(string)
	}
	b, err := jsFS.ReadFile("directives/" + name + ".js")
	s := ""
	if err == nil {
		s = strings.TrimSpace(string(b))
	}
	moduleCache.Store(key, s)
	return s
}

func directiveAsset(name string) *Asset {
	source := directiveSource(name)
	if source == "" {
		return nil
	}
	return cachedAsset("directive:"+name, func() *Asset { return buildAsset("directive", name, source) })
}

// directiveAssets is the directive files a page needs, in a fixed order. The page is read without
// its examples, so a docs page that prints data-kit-intersect gets no observer. In the engine's
// grouped form the observer is asked for by an event — intersect-enter or intersect-leave in a
// data-kitwork-event — so that brings it too.
func directiveAssets(html string) []*Asset {
	var out []*Asset
	for _, name := range []string{"intersect", "drag"} {
		if strings.Contains(html, directiveMarkers[name]) || name == "intersect" && groupedIntersect(html) {
			if asset := directiveAsset(name); asset != nil {
				out = append(out, asset)
			}
		}
	}
	return out
}

// groupedEventAttrRe finds a data-kitwork-event attribute; its value is read by hydrate.Pairs.
var groupedEventAttrRe = regexp.MustCompile(`data-kitwork-event="([^"]*)"`)

// groupedIntersect reports whether a data-kitwork-event on the page has an intersect-enter or
// intersect-leave pair (any modifiers after the name).
func groupedIntersect(html string) bool {
	if !strings.Contains(html, "intersect-") {
		return false
	}
	for _, m := range groupedEventAttrRe.FindAllStringSubmatch(html, -1) {
		pairs, err := hydrate.Pairs(htmlattr.Decode(m[1]))
		if err != nil {
			continue
		}
		for _, pair := range pairs {
			event, _, _ := strings.Cut(pair.Name, ".")
			if event == "intersect-enter" || event == "intersect-leave" {
				return true
			}
		}
	}
	return false
}
