package hydrate

import (
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// PreRenderBind is first paint for COMPONENT state.
//
// A component's state is restored by JavaScript, so anything bound to it is wrong for one frame: a
// sidebar the user collapsed renders full width, then snaps. Reading the state back from
// localStorage does not help — the server cannot see localStorage, and a script cannot run before
// an element in <body> exists. The fix has to come from the server, which means the state has to
// travel in something the server receives: a cookie.
//
// Nothing here knows what a sidebar is. data-kit-bind ALREADY declares which attribute takes which
// expression, so the server evaluates that same expression against the restored state and bakes the
// result into the tag. Any component with persisted state and a bind gets flash-free first paint,
// and markup written for the client needs no changes:
//
//	<header data-kit-component="sidebar" data-kit-alias="$sidebar"
//	        data-kit-bind:data-state="status" data-kit-bind:data-open="drawer">
//
//	cookie kitwork.sidebar=status%3Dcollapsed   →   <header … data-state="collapsed">
//
// The client re-evaluates the same expression at boot and computes the same value, so the baked
// attribute is replaced by an identical one — no second frame, no flicker.
//
// Only keys the cookie carries are in scope. A component with three fields that persists one gets
// that one baked; the rest evaluate as missing, exactly as they do on the client before boot, and
// PreRender never bakes a value it might get wrong.
func PreRenderBind(htmlStr string, state map[string]map[string]any) string {
	if len(state) == 0 || !strings.Contains(htmlStr, "data-kit-component=") {
		return htmlStr
	}
	return componentTagRe.ReplaceAllStringFunc(htmlStr, func(tag string) string {
		cm := componentAttrRe.FindStringSubmatch(tag)
		if cm == nil {
			return tag
		}
		scope, ok := state[ComponentName(authoredAttribute(cm[1]))]
		if !ok || len(scope) == 0 {
			return tag
		}
		// Only a binding whose every read the cookie carries is baked; the rest evaluate as missing
		// on the client too, and PreRender never bakes a value it might get wrong.
		attrs := map[string]any{}
		for _, bm := range bindAttrRe.FindAllStringSubmatch(tag, -1) {
			node, err := compileAuthoredAttribute(bm[2])
			if err != nil || !varsKnown(node, scope) {
				continue
			}
			value, err := Eval(node, scope)
			if err != nil {
				continue
			}
			attrs[strings.ToLower(bm[1])] = value
		}
		if len(attrs) == 0 {
			return tag
		}
		return applyBoundAttrs(tag, attrs)
	})
}

// componentTagRe matches an OPEN TAG carrying data-kit-component. Self-closing and quoted-attribute
// edge cases are not a concern: the value scanned is always a double-quoted attribute, so ">" can
// only end the tag.
var componentTagRe = regexp.MustCompile(`(?is)<[a-z][a-z0-9-]*\b[^>]*\bdata-kit-component="[^"]*"[^>]*>`)

var componentAttrRe = regexp.MustCompile(`(?i)\bdata-kit-component="([^"]*)"`)

// bindAttrRe matches one binding, data-kit-bind:<name>="expr" — the target in the name, one
// expression in the value.
var bindAttrRe = regexp.MustCompile(`(?i)\bdata-kit-bind:([a-z][a-z0-9-]*)="([^"]*)"`)

// ComponentName strips the version tail: "sidebar@v1.0.0" → "sidebar". The alias is its own
// attribute (data-kit-alias), never part of the name.
func ComponentName(decl string) string {
	if i := strings.IndexByte(decl, '@'); i >= 0 {
		decl = decl[:i]
	}
	return strings.TrimSpace(decl)
}

// applyBoundAttrs writes evaluated bindings onto an open tag as the first paint the client will
// compute again: a reflected boolean (disabled, hidden, open…) and the live booleans (checked,
// selected) are present or absent; aria-* is spelled "true"/"false"; anything else is the value,
// and false/null removes it. An attribute already written by the author is REPLACED, so the server
// and the client cannot disagree about what the element says at rest.
func applyBoundAttrs(tag string, attrs map[string]any) string {
	inner := strings.TrimSuffix(tag, ">")
	selfClosing := strings.HasSuffix(inner, "/")
	if selfClosing {
		inner = strings.TrimSuffix(inner, "/")
	}

	for k, v := range attrs {
		if !safeAttrName(k) {
			continue
		}
		inner = removeAttr(inner, k)
		aria := strings.HasPrefix(k, "aria-")
		switch {
		case aria && (v == nil || v == false):
			inner += ` ` + k + `="false"`
		case aria && v == true:
			inner += ` ` + k + `="true"`
		case v == nil || v == false:
			// removed — the client would drop it too
		case v == true:
			inner += ` ` + k + `=""`
		default:
			inner += ` ` + k + `="` + html.EscapeString(attrText(v)) + `"`
		}
	}

	if selfClosing {
		return inner + "/>"
	}
	return inner + ">"
}

// attrText renders a value the way the client's setAttribute would.
func attrText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	default:
		return ""
	}
}

// safeAttrName keeps a computed key from becoming markup. An expression could name anything; only
// plain attribute characters may reach the tag.
func safeAttrName(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		ok := c == '-' || c == '_' || c == ':' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// removeAttr strips an existing name="value" (or bare name) from the inside of an open tag.
func removeAttr(inner, name string) string {
	re := regexp.MustCompile(`(?i)\s+` + regexp.QuoteMeta(name) + `(="[^"]*"|='[^']*'|=[^\s]*)?`)
	return re.ReplaceAllString(inner, "")
}

// CookiePrefix names the cookies a component may persist into. The kernel writes
// kitwork.<component> as a query string ("status=collapsed"), which is cookie-safe, needs no JSON
// encoding, and extends to several keys without a format change.
const CookiePrefix = "kitwork."

// StateFromCookies turns the request's cookies into the scope map PreRenderBind expects. Values
// are decoded as query strings; a cookie that does not parse is skipped rather than guessed at.
//
// Numbers and booleans are recovered, because an expression compares against them: a component
// storing count=3 must evaluate `count > 2` the same on both ends, and "3" > 2 is not that.
func StateFromCookies(cookies map[string]string) map[string]map[string]any {
	if len(cookies) == 0 {
		return nil
	}
	out := map[string]map[string]any{}
	for name, raw := range cookies {
		if !strings.HasPrefix(name, CookiePrefix) {
			continue
		}
		component := strings.TrimPrefix(name, CookiePrefix)
		if component == "" {
			continue
		}
		values, err := url.ParseQuery(raw)
		if err != nil {
			continue
		}
		scope := map[string]any{}
		for k, vs := range values {
			if len(vs) == 0 {
				continue
			}
			scope[k] = coerceCookieValue(vs[0])
		}
		if len(scope) > 0 {
			out[component] = scope
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func coerceCookieValue(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// varsKnown reports whether every scope read in an expression has a value in scope.
func varsKnown(node any, scope map[string]any) bool {
	arr, ok := node.([]any)
	if !ok || len(arr) == 0 {
		return true // a bare literal depends on nothing
	}
	op, _ := arr[0].(string)
	if op == "$" {
		if len(arr) < 2 {
			return false
		}
		name, _ := arr[1].(string)
		_, present := scope[name]
		return present
	}
	for _, child := range arr[1:] {
		sub, ok := child.([]any)
		if !ok {
			continue
		}
		if isNode(sub) {
			if !varsKnown(sub, scope) {
				return false
			}
			continue
		}
		for _, item := range sub {
			if !varsKnown(item, scope) {
				return false
			}
		}
	}
	return true
}
