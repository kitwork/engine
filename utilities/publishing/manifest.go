package publishing

import (
	"encoding/json"
	"strings"

	"github.com/kitwork/engine/value"
)

// ManifestMediaType is the type browsers expect for a web app manifest.
const ManifestMediaType = "application/manifest+json; charset=utf-8"

// Manifest renders a web app manifest from the declared map. Absent keys take
// conservative defaults rather than being omitted, because a manifest missing
// name or start_url is one a browser silently refuses to install.
//
// Both spellings are accepted for the two-word keys — shortName and short_name —
// so a site can write the manifest the way the rest of its config reads and
// still emit the exact names the specification requires.
func Manifest(data value.Value, base string) (string, error) {
	m := map[string]value.Value{}
	if data.IsMap() {
		m = data.Map()
	}

	text := func(fallback string, names ...string) string {
		if v := field(m, names...); v.K == value.String {
			if s := strings.TrimSpace(v.Text()); s != "" {
				return s
			}
		}
		return fallback
	}

	document := map[string]any{
		"name":             text("", "name"),
		"short_name":       text(text("", "name"), "short_name", "shortName"),
		"start_url":        text("/", "start_url", "startUrl", "start"),
		"scope":            text("/", "scope"),
		"display":          text("standalone", "display"),
		"background_color": text("#ffffff", "background_color", "backgroundColor", "background"),
		"theme_color":      text("#ffffff", "theme_color", "themeColor", "theme"),
	}
	if description := text("", "description"); description != "" {
		document["description"] = description
	}
	if lang := text("", "lang", "language"); lang != "" {
		document["lang"] = lang
	}
	if orientation := text("", "orientation"); orientation != "" {
		document["orientation"] = orientation
	}

	// Icons: a plain string is the common case (one source image), an array is
	// the explicit one. An empty list is left out entirely — an icons key with
	// nothing in it is worse than no key at all.
	switch icons := field(m, "icons", "icon"); icons.K {
	case value.String:
		if source := strings.TrimSpace(icons.Text()); source != "" {
			document["icons"] = []map[string]any{{
				"src":     source,
				"sizes":   "any",
				"purpose": "any",
			}}
		}
	case value.Array:
		list := make([]any, 0, len(icons.Array()))
		for _, entry := range icons.Array() {
			switch entry.K {
			case value.String:
				if source := strings.TrimSpace(entry.Text()); source != "" {
					list = append(list, map[string]any{"src": source, "sizes": "any"})
				}
			case value.Map:
				converted := map[string]any{}
				for key, item := range entry.Map() {
					if item.K == value.String {
						converted[key] = item.Text()
					}
				}
				if _, ok := converted["src"]; ok {
					list = append(list, converted)
				}
			}
		}
		if len(list) > 0 {
			document["icons"] = list
		}
	}

	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}
