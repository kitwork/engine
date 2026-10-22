package ui

// The catalog is the vocabulary: what each tag means and what it becomes on the web. Everything in
// markup is a tag (KITWORK_UI §13, 18/10/2026); the name says whose it is:
//   - a name without a dash is Kitwork's: the core vocabulary and Kitwork's own elements (Kitwork
//     decides the element), and Kitwork's components below (<tabs>, <carousel>: host.go);
//   - a name with a dash is the site's: a piece with only a .ui is a layout (<product-card for="…" />,
//     layout.go), a piece with logic, a .work of the site or the identity, a component (<shop-cart>,
//     host.go). A region (<header />) is a layout called by Kitwork's tag.

import hydrate "github.com/kitwork/engine/jit/hydrate"

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
	// a post the site saved, cleaned on the server and shown as it was written (richtext.go): the one
	// way .ui writes HTML that came as data
	"richtext": {html: "div", kind: kindSpecial},

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
	// a group of the buttons of an editor's toolbar, and only there (editor.go): role="group", named by
	// its alt, a line drawn between two
	"group": {html: "div", kind: kindContainer},

	// grouping without an element
	"fragment": {kind: kindSpecial},
}

// plannedElements are Kitwork elements the spec describes (docs/components) that the engine does not
// build yet: written as a tag they are named as such, never looked up as a layout or a component.
var plannedElements = map[string]bool{"dialog": true, "autocomplete": true, "avatar": true, "avatars": true}

// kitworkComponent is one of Kitwork's own components, written as its tag (KITWORK_UI §13):
// <tabs as="$files">…</tabs>, <appearance click="toggle()">…</appearance>, <progressbar />. The tag
// builds one element, the host: a <div> like a box, a <button> for appearance (tag), the root of its
// own UI for progressbar (root).
type kitworkComponent struct {
	// root is the tag Kitwork's own UI is built on; "" when what the author writes inside the host is
	// the component's UI (a component without a UI)
	root string
	// tag is the element of .ui the host is when it is not a box: a button for appearance, what the
	// visitor presses
	tag string
	// state is what the component declares (KITWORK_UI §7), as it is before anything happened: inside
	// the component these names are its own, not the page's — a page that declares open does not open
	// every dropdown
	state map[string]any
	// planned: the spec describes it (docs/components), the engine does not build it yet
	planned bool
	// around: the component has words of its own read off the host and a rule for what is inside it
	// (the terminal: typing, and a prompt before each <code>). The host is the author's tag and keeps
	// its attributes, like a component without a UI; what is inside is markup of the page, compiled
	// with that rule (terminal.go).
	around bool
	// empty: a component without a UI that may be written with nothing inside, because what it does
	// is heard through its events (network: <network offline="…" />)
	empty bool
}

// hostTag is the element of .ui a call of the component builds on.
func (k *kitworkComponent) hostTag() string {
	switch {
	case k.root != "":
		return k.root
	case k.tag != "":
		return k.tag
	}
	return "box"
}

var kitworkComponents = map[string]*kitworkComponent{
	// the viewer's choice is kept in their browser: the server does not know it, so dark is unknown
	// (nil reads as false: the first paint shows the light state and the kernel corrects it). It is
	// what the visitor presses, a button: <appearance click="toggle()">…</appearance>.
	"appearance": {
		state: map[string]any{"mode": "system", "dark": nil, "toggle": nil, "set": nil, "system": nil},
		tag:   "button",
	},
	// a trigger and a popover: a container, never a button (a popover does not sit inside a button)
	"dropdown": {
		state: map[string]any{"open": false, "toggle": nil, "close": nil},
	},
	// the page's stack of notices, at an edge of the screen: <toast as="$toast"> holds
	// the blueprint of a notice the author draws, <card for="notice of notices; key: notice.id"> (toast.go).
	// notices are those on screen, waiting how many older ones wait off it, paused whether time stands
	// still; the page says a notice through the alias ($toast.success('Đã lưu.'))
	"toast": {
		state: map[string]any{"notices": []any{}, "waiting": 0.0, "paused": false,
			"show": nil, "info": nil, "success": nil, "warning": nil, "danger": nil, "loading": nil,
			"update": nil, "dismiss": nil, "clear": nil, "act": nil},
	},
	// how far the move to the next page has got: first in view.ui, above the header
	"progressbar": {root: "box", state: map[string]any{"active": false, "value": 0.0, "top": nil}},
	// commands the author draws a window around, <terminal typing="3s">: each <code>
	// inside is a command (terminal.go). copy() puts them on the clipboard and copied is true for two
	// seconds after; typing is the total time typing them takes, in milliseconds (0: none).
	"terminal": {
		state:  map[string]any{"typing": 0.0, "copied": false, "copy": nil},
		around: true,
	},

	// one panel at a time, chosen by its tab: <tabs as="$files"> holds the tabs, each a
	// <button click="select('router')">; a panel anywhere on the page is an element whose show compares
	// $files.selected with a tab's name (tabs.go). selected is the chosen tab's name (the first tab's
	// when the page starts), persist the name the choice is kept under (kit.storage), "" for none:
	// persist="true" is the set of the tab names, persist="shell" a name the author chose.
	"tabs": {
		state: map[string]any{"selected": "", "persist": "", "select": nil},
	},
	// whether the page can reach the network, and the moment that changes: <network
	// place="bottom-left"> around a notice the author draws (<card show="open">: an icon, the words,
	// <button click="close()">), or empty with only its events (offline="…" online="…"). connected is
	// the browser's word, true on the server's first paint; open whether the notice shows, false there;
	// offline and online are its events (componentEvents); place, delay and duration are its own words,
	// read off the host (network.go).
	"network": {
		state: map[string]any{"connected": true, "open": false, "close": nil},
		empty: true,
	},
	// slides the author draws in a <list>, on a track that scrolls sideways and snaps to each one:
	// <carousel as="$hero" alt="Món nổi bật" duration="5s"> (carousel.go). current is the
	// slide showing (from 0), count how many, playing whether it moves on by itself
	"carousel": {
		state: map[string]any{"current": 0.0, "count": 0.0, "playing": false,
			"go": nil, "next": nil, "previous": nil, "play": nil, "pause": nil, "toggle": nil},
	},
	// time that runs out, the author drawing the time left in one <time>: <countdown as="$tea"
	// duration="5m" ready="play()" finish="…"> (countdown.go). duration is how long it counts and
	// remaining what is left, in milliseconds, hours, minutes and seconds the parts of remaining, playing
	// whether it counts; the server paints the start (all of duration left, not playing). finish is its
	// event (componentEvents)
	"countdown": {
		state: map[string]any{"duration": 0.0, "remaining": 0.0, "hours": 0.0, "minutes": 0.0, "seconds": 0.0, "playing": false,
			"play": nil, "pause": nil, "toggle": nil, "stop": nil},
	},
	// rich text a person writes into a form, the author drawing the toolbars, the link's <popover> and the
	// writing area, one <content name="body" value="{{ post.body }}" /> inside it: <editor as="$body">
	// (editor.go). empty, words and characters are the text's (the server paints them from the value,
	// cleaned), unsaved whether it differs from what the page opened with, open whether the link's
	// popover shows, address what its <input value="{{ address }}"> holds; format how the post is seen and
	// typed ('rich', 'markdown' or 'html': its buttons write it, format = 'markdown'), dropped the tags the
	// last switch from html, a paste or markdown's raw HTML left out (21/10/2026); the methods are its
	// commands
	"editor": {
		state: map[string]any{"empty": true, "words": 0.0, "characters": 0.0, "unsaved": false, "open": false, "address": "",
			"format": "rich", "dropped": []any{},
			"toggle": nil, "align": nil, "rule": nil, "link": nil, "unlink": nil, "undo": nil, "redo": nil, "close": nil},
	},
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
	"online": true, "offline": true, "finish": true,
}

// componentEvents are the events only a Kitwork component sends, each with that component: written
// on any other tag they would never happen, so there they are an error that names the component. The
// kernel's check knows the same list (jit/hydrate).
var componentEvents = hydrate.ComponentEvents

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
	"richtext":   {"value"},
	"form":       {"method"},
	"breadcrumb": {"alt"},
	"badge":      {"alt"},
	"accordions": {"alt"},
	"box":        {"alt"},
	"card":       {"alt"},
	"section":    {"alt"},
	"list":       {"alt"},
	"item":       {"alt"},
	"group":      {"alt"},
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
