package ui

import (
	"encoding/json"
	"html"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kitwork/engine/jit/highlight"
)

// A compiled page is a tree of ops. Everything that does not depend on data is joined into static
// strings once, when the page is prepared; a request only fills the holes: the text of {{ }}, the
// rows of a for, the branches of an if.

type op interface {
	render(r *run, scope map[string]any)
}

// run is the state of one render: the request, and the counters the list and breadcrumb rules need.
type run struct {
	b       strings.Builder
	path    string
	origin  string
	items   int
	selects []any
	crumbs  *[]crumb
	// page is the page being rendered; extra collects the icons only this request's data named, which
	// the skeleton could not list (Page.Document hands them to Request.IconsHead)
	page  *Page
	extra map[string]bool
	// warn tells the author, while developing, of a value from data that makes no class (Request.Warn)
	warn func(string)
	// language is the page's lang ("vi" when empty): the words the engine itself writes are in it
	language string
}

type crumb struct {
	name string
	url  string
}

type staticOp struct {
	s     string
	items int // list items inside the folded HTML, for the empty rule
}

func (o *staticOp) render(r *run, _ map[string]any) {
	r.b.WriteString(o.s)
	r.items += o.items
}

type textOp struct{ in *interpolation }

func (o *textOp) render(r *run, scope map[string]any) {
	r.b.WriteString(html.EscapeString(o.in.render(scope)))
}

type forSpec struct {
	item  string
	index string
	list  *expr
	key   *expr // "; key: …": what tells one row from another
}

type binding struct {
	name string
	in   *interpolation
}

// dynClass is a presentation attribute whose value has {{ }}: each value it can take, to its class.
// The values its expression spells out were each checked when the page compiled; for a value from
// data the attribute's whole domain is in the page's stylesheet (a colour: every declared token).
// A value outside the map makes no class: data never writes CSS of its own.
type dynClass struct {
	name    string
	in      *interpolation
	classes map[string]string
	// icon is the value of an icon from data: the class is icon-<value>, its CSS the icons pass's
	icon bool
	// language is the language= of a code element from {{ }}: the class is language-<value> for a
	// language jit/highlight has, none for any other value (its code shows plain, and the code's own op
	// tells the author: code.go)
	language bool
	// where and written name the attribute for the development warning: file:line:col, name="…"
	where, written string
}

// classOf is the class one value makes; ok is false for a value outside what the attribute takes.
// No value is no class.
func (d dynClass) classOf(value string) (string, bool) {
	if value == "" {
		return "", true
	}
	if d.icon {
		if !iconName(value) {
			return "", false
		}
		return "icon-" + value, true
	}
	if d.language {
		if value = strings.TrimSpace(value); highlight.Has(value) {
			return languageClass(value), true
		}
		return "", true
	}
	class, ok := d.classes[value]
	return class, ok
}

// values lists the values the attribute takes, sorted, for the warning.
func (d dynClass) values() []string {
	out := make([]string, 0, len(d.classes))
	for value := range d.classes {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

type dynAttr struct {
	name    string
	in      *interpolation
	boolean bool
	image   bool // an image's src: a value from data that is not an image address is not written
}

// input is one input of an instance the kernel runs: its name and, when its value reads a live
// component around the host, the expression the kernel evaluates ("" writes the value as a literal).
type input struct {
	name   string
	in     *interpolation
	source string
}

// elemOp is one element that depends on data somewhere: a hole in an attribute, a for, an if, a
// show, or a child that does.
type elemOp struct {
	tag         string
	staticClass []string
	dynClasses  []dynClass
	staticAttrs string // pre-rendered ` name="value"` pairs
	dynAttrs    []dynAttr
	children    []op
	void        bool

	forEach  *forSpec
	bindings []binding
	ifExpr   *expr
	showExpr *expr

	// component host: the call's if and show, and the values of its inputs (bindings), are read where
	// the call stands; then the names the component declares cover the page's for everything inside it
	// (its own show, attributes and children)
	callerIf   *expr
	callerShow *expr
	state      map[string]any
	// inputs is the part of bindings the kernel seeds the instance with: written per request as
	// data-kitwork-scope, each value a literal (page data) or the expression itself when it reads a
	// live component around the host (the kernel runs it in the scope around the host)
	inputs []input

	// link rule: aria-current from the request path
	href   *interpolation
	exact  bool
	kept   bool // outside <content />: the client re-marks it after a soft navigation
	newTab bool // new-tab="true": target="_blank", rel="noopener" and a note for screen readers

	// list rule: the empty part shows only when no item was rendered
	countsItem bool
	emptyOps   []op

	// select/option rule: the option equal to the select's value is selected
	selectValue *interpolation
	optionValue *interpolation

	// breadcrumb: collect names and links for the BreadcrumbList data
	breadcrumb   bool
	crumbLink    bool
	crumbCurrent bool
	label        string // aria-label for the breadcrumb navigation

	// textarea: value is the content
	content *interpolation

	// fragment: no element of its own
	fragment bool
}

func (o *elemOp) render(r *run, scope map[string]any) {
	if o.forEach == nil {
		o.renderOne(r, scope)
		return
	}
	list, _ := o.forEach.list.eval(scope).([]any)
	oldItem, hadItem := scope[o.forEach.item]
	oldIndex, hadIndex := scope[o.forEach.index]
	for i, item := range list {
		scope[o.forEach.item] = item
		if o.forEach.index != "" {
			scope[o.forEach.index] = float64(i)
		}
		o.renderOne(r, scope)
	}
	restore(scope, o.forEach.item, oldItem, hadItem)
	if o.forEach.index != "" {
		restore(scope, o.forEach.index, oldIndex, hadIndex)
	}
}

func restore(scope map[string]any, name string, old any, had bool) {
	if had {
		scope[name] = old
	} else {
		delete(scope, name)
	}
}

func (o *elemOp) renderOne(r *run, scope map[string]any) {
	// read where the call stands: whether the host is there, whether it shows, what goes in
	if o.callerIf != nil && !truthy(o.callerIf.eval(scope)) {
		return
	}
	hidden := o.callerShow != nil && !truthy(o.callerShow.eval(scope))
	var inputs string
	if len(o.inputs) > 0 {
		inputs = o.inputPairs(scope)
	}
	if len(o.bindings) > 0 {
		saved := make([]any, len(o.bindings))
		had := make([]bool, len(o.bindings))
		values := make([]any, len(o.bindings))
		for i, b := range o.bindings {
			values[i] = b.in.value(scope)
		}
		for i, b := range o.bindings {
			saved[i], had[i] = scope[b.name]
			scope[b.name] = values[i]
		}
		defer func() {
			for i, b := range o.bindings {
				restore(scope, b.name, saved[i], had[i])
			}
		}()
	}
	if o.state != nil {
		saved := make(map[string]any, len(o.state))
		had := make(map[string]bool, len(o.state))
		for name, v := range o.state {
			saved[name], had[name] = scope[name]
			scope[name] = v
		}
		defer func() {
			for name := range o.state {
				restore(scope, name, saved[name], had[name])
			}
		}()
	}
	if o.ifExpr != nil && !truthy(o.ifExpr.eval(scope)) {
		return
	}
	if o.fragment {
		o.renderChildren(r, scope)
		return
	}
	if o.countsItem {
		r.items++
	}

	var crumbs []crumb
	if o.breadcrumb {
		outer := r.crumbs
		r.crumbs = &crumbs
		defer func() { r.crumbs = outer }()
	}

	r.b.WriteByte('<')
	r.b.WriteString(o.tag)
	o.writeClass(r, scope)
	r.b.WriteString(o.staticAttrs)
	if inputs != "" {
		writeAttr(&r.b, "data-kitwork-scope", inputs)
	}
	if o.label != "" {
		writeAttr(&r.b, "aria-label", o.label)
	}
	var href string
	if o.href != nil {
		href = o.href.render(scope)
		if !safeLink(href) {
			// a link from data to something that is not a page (javascript:, data:…) is not written:
			// the words stay, there is nothing to click
			href = ""
		}
	}
	if href != "" {
		writeAttr(&r.b, "href", href)
		external := externalLink(href, r.origin)
		if o.newTab {
			writeAttr(&r.b, "target", "_blank")
		}
		switch {
		case external:
			writeAttr(&r.b, "rel", "external noopener")
		case o.newTab:
			writeAttr(&r.b, "rel", "noopener")
		}
		switch current := linkCurrent(r.path, href, o.exact); current {
		case "page", "true":
			writeAttr(&r.b, "aria-current", current)
		}
		if o.kept && internalLink(href) {
			// Drive swaps only <content />; this link stays, so it says how to mark it again
			mode := ""
			if o.exact {
				mode = "exact"
			}
			writeAttr(&r.b, "data-kitwork-current", mode)
		}
	}
	for _, a := range o.dynAttrs {
		v := a.in.value(scope)
		if a.boolean {
			if truthy(v) {
				r.b.WriteByte(' ')
				r.b.WriteString(a.name)
			}
			continue
		}
		if a.image && !safeImage(stringify(v)) {
			continue
		}
		writeAttr(&r.b, a.name, stringify(v))
	}
	if o.optionValue != nil && len(r.selects) > 0 {
		if stringify(r.selects[len(r.selects)-1]) == o.optionValue.render(scope) {
			r.b.WriteString(" selected")
		}
	}
	// a false show is the hidden attribute, the way the kernel hides (el.hidden): a show the browser
	// keeps up to date can always reveal what the server hid, and the CSS makes [hidden] win over any
	// display a class gives
	if hidden || o.showExpr != nil && !truthy(o.showExpr.eval(scope)) {
		r.b.WriteString(` hidden`)
	}
	if o.crumbCurrent {
		r.b.WriteString(` aria-current="page"`)
	}
	r.b.WriteByte('>')
	if o.void {
		return
	}

	if o.selectValue != nil {
		r.selects = append(r.selects, o.selectValue.value(scope))
		defer func() { r.selects = r.selects[:len(r.selects)-1] }()
	}

	start := r.b.Len()
	if o.content != nil {
		r.b.WriteString(html.EscapeString(o.content.render(scope)))
	}
	if o.tag == "ul" || o.tag == "ol" {
		o.renderList(r, scope)
	} else {
		o.renderChildren(r, scope)
	}
	if (o.crumbLink || o.crumbCurrent) && r.crumbs != nil {
		name := plainText(r.b.String()[start:])
		url := href
		if o.crumbCurrent {
			url = r.path
		}
		*r.crumbs = append(*r.crumbs, crumb{name: name, url: url})
	}
	if o.newTab && href != "" {
		r.b.WriteString(newTabNote(r.language))
	}
	r.b.WriteString("</")
	r.b.WriteString(o.tag)
	r.b.WriteByte('>')

	if o.breadcrumb && len(crumbs) > 0 {
		writeBreadcrumbData(r, crumbs)
	}
}

// inputPairs are an instance's inputs in the grouped form the kernel seeds it from
// (data-kitwork-scope="open: true; label: 'Tài khoản'"): page data as a literal, read now.
func (o *elemOp) inputPairs(scope map[string]any) string {
	pairs := make([]string, 0, len(o.inputs))
	for _, in := range o.inputs {
		value := in.source
		if value == "" {
			value = literal(in.in.value(scope), 0)
		}
		pairs = append(pairs, in.name+": "+value)
	}
	return strings.Join(pairs, "; ")
}

// literal writes a value as an expression of the grammar the kernel reads: what the server read from
// the page's data, handed to the browser as it is. Past a depth, or for what has no literal (a
// function), it is null.
func literal(v any, depth int) string {
	return literalWithin(v, depth, nil)
}

// literalID is what an object or array is: its map, or its slice's first element and length.
type literalID struct {
	pointer uintptr
	length  int
}

// literalWithin is literal inside the objects and arrays on path, the ones v sits in. One of them met
// again is a value that holds itself — {{ x = $ }} puts the page's scope into itself — and it is null
// there: before, it was written again to the depth bound, and each key back to itself doubled what was
// written at every level, three of them some 15 GB for one request (14/10/2026).
func literalWithin(v any, depth int, path []literalID) string {
	if depth > 16 {
		return "null"
	}
	var id literalID
	switch t := v.(type) {
	case []any:
		if len(t) > 0 {
			id = literalID{pointer: reflect.ValueOf(t).Pointer(), length: len(t)}
		}
	case map[string]any:
		if t != nil {
			id = literalID{pointer: reflect.ValueOf(t).Pointer(), length: -1}
		}
	}
	if id != (literalID{}) {
		for _, on := range path {
			if on == id {
				return "null"
			}
		}
		path = append(path, id)
	}
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return "null"
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case string:
		return quoteString(t)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = literalWithin(x, depth+1, path)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = quoteString(k) + ": " + literalWithin(t[k], depth+1, path)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return "null"
}

func (o *elemOp) renderChildren(r *run, scope map[string]any) {
	for _, child := range o.children {
		child.render(r, scope)
	}
}

func (o *elemOp) renderList(r *run, scope map[string]any) {
	outer := r.items
	r.items = 0
	o.renderChildren(r, scope)
	if r.items == 0 {
		for _, child := range o.emptyOps {
			child.render(r, scope)
		}
	}
	r.items = outer
}

func (o *elemOp) writeClass(r *run, scope map[string]any) {
	classes := o.staticClass
	if len(o.dynClasses) > 0 {
		classes = append([]string(nil), o.staticClass...)
		for _, d := range o.dynClasses {
			value := stringify(d.in.value(scope))
			class, ok := d.classOf(value)
			if !ok {
				// a value from data the attribute does not take (a colour that is no token of the page's
				// palette, a word off the scale): no class, so data never reaches the CSS; the author hears
				// of it while developing
				r.warnValue(d, value)
				continue
			}
			if class == "" {
				continue
			}
			if d.icon && r.page != nil && !r.page.covered(class) {
				// an icon named only by this request's data: its CSS is the request's own icons file
				// (Request.IconsHead)
				if r.extra == nil {
					r.extra = map[string]bool{}
				}
				r.extra[class] = true
			}
			classes = append(classes, class)
		}
	}
	if len(classes) == 0 {
		return
	}
	writeAttr(&r.b, "class", strings.Join(classes, " "))
}

// warnValue says, once per page, that a value from data made no class.
func (r *run) warnValue(d dynClass, value string) {
	if r.warn == nil || r.page == nil {
		return
	}
	message := d.where + ": " + d.written + " cho \"" + value + "\": "
	if d.icon {
		message += "tên icon chỉ gồm chữ thường, số và dấu gạch nối; không có class"
	} else {
		message += "không phải giá trị " + d.name + " nhận; không có class. Nhận: " + strings.Join(d.values(), ", ")
	}
	if r.page.warned.first(message) {
		r.warn(message)
	}
}

// iconName reports whether a value from data can name an icon: lowercase letters and digits in words
// joined by hyphens (shopping-cart, number-1), nothing a selector or a URL could be made of.
func iconName(value string) bool {
	if value == "" || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// warnings are the messages a page has given, each once.
type warnings struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (w *warnings) first(message string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen[message] {
		return false
	}
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	w.seen[message] = true
	return true
}

func writeAttr(b *strings.Builder, name, value string) {
	b.WriteByte(' ')
	b.WriteString(name)
	b.WriteString(`="`)
	b.WriteString(html.EscapeString(value))
	b.WriteByte('"')
}

// linkCurrent says whether a link points at the page being viewed: "page" for the page itself,
// "true" for a branch the page sits inside (except "/", which only matches itself), "" otherwise.
func linkCurrent(path, href string, exact bool) string {
	if path == "" || href == "" || strings.Contains(href, "://") || strings.HasPrefix(href, "#") {
		return ""
	}
	clean := func(s string) string {
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			s = s[:i]
		}
		if len(s) > 1 {
			s = strings.TrimSuffix(s, "/")
		}
		return s
	}
	p, h := clean(path), clean(href)
	if p == h {
		return "page"
	}
	if exact || h == "/" {
		return ""
	}
	if strings.HasPrefix(p, h+"/") {
		return "true"
	}
	return ""
}

// internalLink reports whether href is a path on this site ("/menu"), the only links a page can be.
func internalLink(href string) bool {
	return strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//")
}

// linkScheme is the scheme a URL starts with, lower case, read the way a browser reads it: tabs and
// line breaks inside are dropped and leading spaces and control characters ignored ("java\tscript:" is
// javascript:). "" for a path, an anchor or a query.
func linkScheme(value string) string {
	var b strings.Builder
	for _, c := range value {
		if c == '\t' || c == '\n' || c == '\r' || (b.Len() == 0 && c <= ' ') {
			continue
		}
		b.WriteRune(c)
	}
	v := b.String()
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == ':':
			if i == 0 {
				return ""
			}
			return strings.ToLower(v[:i])
		case c == '/' || c == '?' || c == '#':
			return ""
		case i == 0 && !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'):
			return ""
		case !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
			return ""
		}
	}
	return ""
}

// safeLink: a link goes to a page in the site, a web page, an e-mail or a phone number (KITWORK_UI §7,
// "Link ra ngoài"). Anything else (javascript:, data:, vbscript:…) is not a place to go.
func safeLink(href string) bool {
	switch linkScheme(href) {
	case "", "http", "https", "mailto", "tel":
		return true
	}
	return false
}

// safeImage: an image comes from the site, the web, or data:image/….
func safeImage(src string) bool {
	switch linkScheme(src) {
	case "", "http", "https":
		return true
	case "data":
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(src)), "data:image/")
	}
	return false
}

// externalLink: a web address on another host than the page's own (origin), or any web address when
// the origin is not known. Such a link leaves the site: rel="external noopener".
func externalLink(href, origin string) bool {
	scheme := linkScheme(href)
	if scheme != "http" && scheme != "https" && !strings.HasPrefix(href, "//") {
		return false
	}
	host := hostOf(href)
	return host == "" || origin == "" || !strings.EqualFold(host, hostOf(origin))
}

func hostOf(u string) string {
	rest := u
	if i := strings.Index(rest, "//"); i >= 0 {
		rest = rest[i+2:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	return rest
}

// newTabNote tells a screen reader the link opens a new tab; it is not seen.
func newTabNote(language string) string {
	words := "(mở trong tab mới)"
	if language != "" && !strings.HasPrefix(strings.ToLower(language), "vi") {
		words = "(opens in a new tab)"
	}
	return `<span style="position:absolute;width:1px;height:1px;margin:-1px;padding:0;border:0;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap"> ` + words + `</span>`
}

// plainText strips tags and collapses whitespace: the name of a breadcrumb entry.
func plainText(s string) string {
	var b strings.Builder
	inTag := false
	for _, c := range s {
		switch {
		case c == '<':
			inTag = true
		case c == '>':
			inTag = false
		case !inTag:
			b.WriteRune(c)
		}
	}
	return strings.Join(strings.Fields(html.UnescapeString(b.String())), " ")
}

func writeBreadcrumbData(r *run, crumbs []crumb) {
	type entry struct {
		Type     string `json:"@type"`
		Position int    `json:"position"`
		Name     string `json:"name"`
		Item     string `json:"item,omitempty"`
	}
	list := struct {
		Context string  `json:"@context"`
		Type    string  `json:"@type"`
		Items   []entry `json:"itemListElement"`
	}{Context: "https://schema.org", Type: "BreadcrumbList"}
	for i, c := range crumbs {
		url := c.url
		if strings.HasPrefix(url, "/") {
			url = r.origin + url
		}
		list.Items = append(list.Items, entry{Type: "ListItem", Position: i + 1, Name: c.name, Item: url})
	}
	data, err := json.Marshal(list)
	if err != nil {
		return
	}
	// "</" inside the data cannot end the script element: escape it.
	safe := strings.ReplaceAll(string(data), "</", `<\/`)
	r.b.WriteString(`<script type="application/ld+json">`)
	r.b.WriteString(safe)
	r.b.WriteString(`</script>`)
}
