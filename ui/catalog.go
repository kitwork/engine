package ui

// The catalog is the vocabulary: what each tag means and what it becomes on the web. Markup has three
// forms and no fourth (KITWORK_UI §13), and the author's one test is "has logic → component=; only
// markup → layout tag; Kitwork's own element → tag":
//   - a Kitwork tag: the core vocabulary and Kitwork's own elements (Kitwork decides the element);
//   - a layout, called by its tag: a region (<header />) or a piece of the site with only a .ui
//     (<product-card for="…" />, layout.go) — a tag outside the catalog is looked up as one;
//   - component="name" on a tag for a piece with logic (host.go): a .work of the site or the identity,
//     or one of Kitwork's components below.

type tagKind uint8

const (
	kindContainer tagKind = iota // lays out children: flex, column by default
	kindGrid                     // two-dimensional grid
	kindInline                   // holds inline content only (text inside becomes <span>)
	kindVoid                     // never has children
	kindSpecial                  // compiled by its own rule
)

type tagInfo struct {
	html   string
	kind   tagKind
	region bool // a page region: <header />, <navbar />… (file named after it, root of that tag)
}

var catalog = map[string]tagInfo{
	// page and regions
	"page":    {html: "body", kind: kindContainer},
	"header":  {html: "header", kind: kindContainer, region: true},
	"footer":  {html: "footer", kind: kindContainer, region: true},
	"navbar":  {html: "nav", kind: kindContainer, region: true},
	"content": {html: "main", kind: kindContainer, region: true},
	"overlay": {html: "div", kind: kindContainer, region: true},
	"sidebar": {html: "aside", kind: kindContainer, region: true},
	"tabbar":  {html: "nav", kind: kindContainer, region: true},

	// what a screen reader says aloud: the new page's title after a move, a sentence a page says
	"reader": {html: "div", kind: kindSpecial},

	// layout and surfaces
	"section": {html: "section", kind: kindContainer},
	"box":     {html: "div", kind: kindContainer},
	"card":    {html: "div", kind: kindContainer},
	"grid":    {html: "div", kind: kindGrid},
	"list":    {html: "ul", kind: kindContainer},
	"item":    {html: "li", kind: kindContainer},
	"empty":   {html: "li", kind: kindContainer},

	// text
	"text":  {kind: kindSpecial},
	"code":  {kind: kindSpecial},
	"quote": {kind: kindSpecial},
	"time":  {html: "time", kind: kindInline},
	"badge": {html: "span", kind: kindInline},

	// actions and media
	"link":   {html: "a", kind: kindInline},
	"button": {html: "button", kind: kindInline},
	"icon":   {html: "i", kind: kindVoid},
	"image":  {html: "img", kind: kindVoid},

	// forms
	"form":     {html: "form", kind: kindContainer},
	"field":    {html: "div", kind: kindContainer},
	"label":    {html: "label", kind: kindInline},
	"hint":     {html: "p", kind: kindInline},
	"error":    {html: "p", kind: kindInline},
	"input":    {html: "input", kind: kindVoid},
	"select":   {html: "select", kind: kindSpecial},
	"option":   {html: "option", kind: kindInline},
	"textarea": {html: "textarea", kind: kindSpecial},

	// Kitwork's own elements ("phần tử của Kitwork"): Kitwork decides the element they become
	"accordions": {html: "div", kind: kindContainer},
	"accordion":  {html: "details", kind: kindSpecial},
	"breadcrumb": {html: "nav", kind: kindSpecial},
	// the floating surface of a component that opens (dropdown…): closed when the page is built
	"popover": {html: "div", kind: kindContainer},

	// grouping without an element
	"fragment": {kind: kindSpecial},
}

// plannedElements are Kitwork elements the spec describes (docs/components) that the engine does not
// build yet: written as a tag they are named as such, never looked up as a layout or a component.
var plannedElements = map[string]bool{"dialog": true, "autocomplete": true, "avatar": true, "avatars": true}

// kitworkComponent is one of Kitwork's own components: written component="name" on a tag the author
// chooses (appearance, dropdown), or on the tag its own UI is built on (toast, progressbar: the call
// writes that tag, empty).
type kitworkComponent struct {
	// root is the tag Kitwork's own UI is built on; "" when what the author writes inside the host is
	// the component's UI (a component without a UI)
	root string
	// state is what the component declares (KITWORK_UI §7), as it is before anything happened: inside
	// the component these names are its own, not the page's — a page that declares open does not open
	// every dropdown
	state map[string]any
	// hosts are the tags a component without a UI may be written on
	hosts map[string]bool
	// planned: the spec describes it (docs/components), the engine does not build it yet
	planned bool
	// around: the component has words of its own read off the host and a rule for what is inside it
	// (the terminal: typing, and a prompt before each <code>). The host is the author's tag and keeps
	// its attributes, like a component without a UI; what is inside is markup of the page, compiled
	// with that rule (terminal.go).
	around bool
}

// containerHosts are the tags a component that holds other content can be written on.
var containerHosts = map[string]bool{"box": true, "card": true, "section": true, "item": true}

var kitworkComponents = map[string]*kitworkComponent{
	// the viewer's choice is kept in their browser: the server does not know it, so dark is unknown
	// (nil reads as false: the first paint shows the light state and the kernel corrects it). It is
	// written on what the visitor presses: <button component="appearance" click="toggle()">.
	"appearance": {
		state: map[string]any{"mode": "system", "dark": nil, "toggle": nil, "set": nil, "system": nil},
		hosts: map[string]bool{"box": true, "card": true, "section": true, "item": true, "button": true},
	},
	// a trigger and a popover: a container, never a button (a popover does not sit inside a button)
	"dropdown": {
		state: map[string]any{"open": false, "toggle": nil, "close": nil},
		hosts: containerHosts,
	},
	// the page's notification stack, filled when something is said ($toast.show(…))
	"toast": {root: "box", state: map[string]any{"visible": false, "message": "", "show": nil, "hide": nil}},
	// how far the move to the next page has got: first in page.ui, above the header
	"progressbar": {root: "box", state: map[string]any{"active": false, "value": 0.0, "top": nil}},
	// commands the author draws a window around, <box component="terminal" typing="3s">: each <code>
	// inside is a command (terminal.go). copy() puts them on the clipboard and copied is true for two
	// seconds after; typing is the total time typing them takes, in milliseconds (0: none).
	"terminal": {
		state:  map[string]any{"typing": 0.0, "copied": false, "copy": nil},
		hosts:  containerHosts,
		around: true,
	},

	"tabs":       {planned: true},
	"tooltip":    {planned: true},
	"loadmore":   {planned: true},
	"pagination": {planned: true},
}

// builtinState is what a Kitwork component declares, as it is before anything happened.
func builtinState(name string) map[string]any {
	if k := kitworkComponents[name]; k != nil {
		return k.state
	}
	return map[string]any{}
}

// inlineHTML are the elements whose content model is phrasing only: a text inside them is a <span>.
var inlineHTML = map[string]bool{
	"a": true, "button": true, "label": true, "span": true, "summary": true, "p": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "time": true,
	"q": true, "s": true, "code": true, "option": true,
}

// events are the event names an attribute may carry before its ':' (input:debounce(300)).
var events = map[string]bool{
	"click": true, "double-click": true, "context-menu": true, "click-outside": true,
	"keydown": true, "keyup": true,
	"pointer-enter": true, "pointer-leave": true, "pointer-down": true, "pointer-up": true,
	"focus-in": true, "focus-out": true,
	"intersect-enter": true, "intersect-leave": true,
	"submit": true, "input": true, "change": true, "scroll": true,
	"load": true, "error": true, "toggle": true, "ready": true,
}

// booleanAttributes are attributes whose value is "true", "false" or {{ }}; never written bare.
var booleanAttributes = map[string]bool{
	"required": true, "readonly": true, "disabled": true, "open": true, "checked": true,
	"multiple": true, "autofocus": true, "exact": true, "italic": true, "underline": true,
	"strikethrough": true, "wrap": true, "selected": true, "suggest": true, "reload": true, "new-tab": true,
}

// passThrough are attributes copied to the HTML element as written (after {{ }}), per tag.
var passThrough = map[string][]string{
	"input":    {"type", "name", "min", "max", "step", "pattern", "placeholder", "required", "readonly", "disabled", "autofocus", "checked", "autocomplete", "inputmode"},
	"select":   {"name", "required", "disabled", "multiple"},
	"textarea": {"name", "placeholder", "required", "readonly", "disabled", "rows"},
	"button":   {"name", "disabled"},
	"option":   {"disabled"},
	"form":     {"name"},
}

// tagWords are the attributes a tag reads besides control (for, if, show), events and presentation.
// On the host of a component without a UI they stay the tag's: an attribute that is none of these is
// an input of the component, and one the component also declares is an error, never a guess.
var tagWords = map[string][]string{
	"link":       {"value", "exact", "reload", "new-tab"},
	"image":      {"value", "alt"},
	"icon":       {"value", "alt"},
	"button":     {"expanded", "type", "value"},
	"input":      {"alt", "type", "value"},
	"select":     {"alt", "value"},
	"option":     {"value"},
	"textarea":   {"alt", "value"},
	"time":       {"value"},
	"text":       {"heading", "strikethrough", "alt"},
	"code":       {"language"},
	"quote":      {"source"},
	"accordion":  {"open"},
	"breadcrumb": {"alt"},
	"badge":      {"alt"},
	"accordions": {"alt"},
	"box":        {"alt"},
	"card":       {"alt"},
	"section":    {"alt"},
	"list":       {"alt"},
	"item":       {"alt"},
}

// tagWord reports whether name is one of a tag's own attributes (tagWords, or copied to the HTML as
// written: passThrough).
func tagWord(tag, name string) bool {
	for _, w := range tagWords[tag] {
		if w == name {
			return true
		}
	}
	for _, w := range passThrough[tag] {
		if w == name {
			return true
		}
	}
	return false
}
