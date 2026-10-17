package ui

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// The tabs (decisions of 16/10/2026, docs/components/tabs.md) are one of Kitwork's components, written
// on a container the author chooses. The author writes the tabs and the panels in markup, the way they
// would anyway; the engine recognises them and writes the accessibility wiring itself:
//
//	<box component="tabs" as="$shell" persist="true" alt="Shell">
//	  <button click="select('bash')">bash</button>
//	  <button click="select('powershell')">PowerShell</button>
//	</box>
//	<box show="$shell.selected == 'bash'">…</box>
//	<box show="$shell.selected == 'powershell'">…</box>
//
//   - The host is the tablist: role="tablist". It holds the tabs; the panels stand anywhere on the page
//     (in a terminal's screen, below a bar), so they read the choice through the alias.
//   - A tab is a <button> inside the host whose click is select('<name>'), a name written out: role="tab",
//     the name as its value (what the kernel's tabs reads), and aria-selected and tabindex bound to
//     selected == '<name>' — the chosen tab is the one stop in the tab order, the arrows move along.
//   - A panel is an element whose show compares the alias's selected with a tab's name
//     ($files.selected == 'router', === too, either side): role="tabpanel", tabindex="0" (a panel of
//     code holds nothing focusable), and the two ids that tie it to its tab — the tab's aria-controls,
//     the panel's aria-labelledby. An id the author wrote is kept; the engine's are kitwork-tab-<n>-<i>
//     and kitwork-tabpanel-<n>-<i>. They are wired once the whole page is compiled, so a panel before its
//     tablist is one too.
//   - selected starts as the first tab's name, unless the call chose one (selected="page"): it goes in
//     the host's data-kitwork-scope, so the server paints that tab chosen and its panels shown and the
//     kernel starts from it. A tabs that persists under a name an earlier tabs of the page has, and
//     chose nothing, starts on the tab that one starts on when it has it (powershell, bash after bash,
//     powershell): the tabs that follow one choice agree from the first frame, before anything is kept.
//   - persist (16/10/2026, the word of the tabs, written out; tabsWords) keeps the choice in the viewer's
//     storage (kit.storage), under a name the engine hands the instance as persist: persist="true" is
//     the SET of the tab names, sorted and joined by "+" (bash+powershell), so every tabs of the site
//     with the same tabs follows one choice, on every page and every visit, with nothing to name;
//     persist="shell" is that name, for sets that differ but should share. persist="false" or none:
//     each tabs chooses on its own, and a reload shows the first tab. remember, the word before, is an
//     error that names persist; there is no group: tabs with the same name follow each other.
//   - A tabs that persists has a pre-paint: a few lines of script right after the last tablist or panel
//     of its name (tabsPrepaint), which shows the tab the viewer's browser kept before the first frame,
//     as the appearance pre-paint does light or dark: the panels' hidden, the tabs' aria-selected and
//     tabindex, and so a tab's look written with selected: (its CSS follows aria-selected). It reads
//     what kit.storage writes in a browser; in a desktop or mobile app kit.storage is the native bridge,
//     which nothing at the first frame can read: the kernel restores once the store answers — and no
//     shell keeps storage.* yet, so an app keeps nothing (docs/components/tabs.md, Còn mở).
//     One constant body, one CSP hash (TabsPrepaintHash).
//
// Nothing here asks the author to think about ARIA: the buttons and the shows they would write anyway
// are how the engine knows the tabs and the panels. What does not fit is told: a tabs with no tab is
// an error, a tab named twice or written in a for, a value on a tab, a <code> as a panel (its show does
// not follow state in the browser yet), a panel in a for; a tab without a panel, a panel naming no tab
// and a tabs without as (nothing outside the host can read it) are warnings, and so is anything a
// person can press or type in inside the host that is no tab (a tablist holds tabs only: the Copy
// button stands beside it).

// tabsScope is one tabs host: what ctx.tabs points to inside it.
type tabsScope struct {
	number  int         // its place among the page's tablists: the ids the engine writes
	alias   string      // its as, "" for none
	aliases *aliasFrame // where its as is set: the page, or one instance's own .ui (each instance its own)
	persist string      // the name its choice is kept under (tabsKey), "" for none
	start   string      // the tab it starts on when the page is compiled, "" when its selected is read from data
	node    *Node
	o       *elemOp  // its element: the pre-paint may follow it
	seq     int      // where it starts among the page's tablists and panels, in document order
	forAt   *forSite // the for around the host or on it: a tab inside another for would repeat
	names   []string // its tabs' names, in order, read off the markup before it compiles
	tabs    []*tabButton
}

// tabButton is one tab as compiled: its button, to which the ids are added once the page is compiled.
type tabButton struct {
	name   string
	o      *elemOp
	id     string // the id the author wrote, "" for none
	node   *Node
	click  *Attr
	htmlID string      // its id: the author's or the engine's
	panels []string    // the ids of its panels
	wired  []*tabPanel // its panels
}

// tabPanel is one element whose show compares an alias's selected with a name.
type tabPanel struct {
	alias, name string
	aliases     *aliasFrame // the aliases in reach where it stands: its alias is the nearest that sets it
	o           *elemOp     // nil for an element whose show does not follow state in the browser (<code>)
	node        *Node
	show        *Attr
	id          string // the id the author wrote, "" for none
	inFor       bool
	seq         int // where it starts among the page's tablists and panels, in document order
}

func (s *tabsScope) tab(name string) (*tabButton, int) {
	for i, t := range s.tabs {
		if t.name == name {
			return t, i
		}
	}
	return nil, -1
}

// tabsHost reads the tabs of a host before it compiles — the buttons whose click is select('<name>'),
// outside any component nested in it — and gives the instance the first one's name as selected, unless
// the call chose one, which must then be one of them.
//
// persist is what tabsWords read: "" for none, "true" for the set of the tab names, else the name
// written. The name it comes to is handed to the instance as persist, beside selected.
func (c *compiler) tabsHost(n *Node, site *callSite, inside ctx, persist string) (*tabsScope, error) {
	scope := &tabsScope{number: c.next("tabs"), seq: c.next("tabs order"), alias: site.alias, aliases: inside.aliases, node: n, forAt: inside.forAt}
	seen := map[string]*Attr{}
	var scan func(nodes []*Node) error
	scan = func(nodes []*Node) error {
		for _, ch := range nodes {
			if ch.IsText() || attrNamed(ch, "component").Name == "component" {
				continue // a nested component's buttons are its own
			}
			tab := false
			if a := attrNamed(ch, "click"); ch.Tag == "button" && a.Name == "click" {
				if e, err := compileExpr(a.Value, ch, a); err == nil {
					if name, ok := tabName(e.ir); ok {
						if first, twice := seen[name]; twice {
							return errAttr(ch, a, "tab %s đã có ở dòng %d: mỗi tab một tên", name, first.Line)
						}
						seen[name] = a
						scope.names = append(scope.names, name)
					}
					tab = selectCall(e.ir) // a select(…) whose name is not written out is told by tabButton
				}
			}
			if !tab && pressable(ch) {
				// role="tablist" holds tabs only (ARIA): the arrows skip anything else, and a screen reader
				// reads it as part of the tab list
				c.warn(ch.File, ch.Line, ch.Column, fmt.Sprintf(`<%s> trong thẻ chủ của tabs không phải tab: dải tab chỉ chứa tab (role="tablist"), phím mũi tên bỏ qua nó mà trình đọc màn hình đọc nó như một phần của dải; đặt nó ra ngoài thẻ chủ, bên cạnh dải tab`, ch.Tag))
			}
			if err := scan(ch.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := scan(n.Children); err != nil {
		return nil, err
	}
	if len(scope.names) == 0 {
		return nil, errAt(n, `tabs cần ít nhất một tab bên trong thẻ chủ: <button click="select('tên')">…</button> (mỗi nút gọi select với một tên viết sẵn là một tab)`)
	}
	c.tabHosts = append(c.tabHosts, scope)
	if persist != "" {
		scope.persist = tabsKey(persist, scope.names)
		givePersist(site, scope.persist)
	}
	for _, b := range site.bindings {
		if b.name != "selected" {
			continue
		}
		if b.in.static() && !containsName(scope.names, b.in.literal()) {
			return nil, errAttr(n, attrNamed(n, "selected"), "selected=\"%s\" không phải tên của tab nào (%s)", b.in.literal(), strings.Join(scope.names, ", "))
		}
		if b.in.static() {
			scope.start = b.in.literal()
		}
		return scope, nil
	}
	// the first tab: what the server paints chosen, and what the kernel starts from — unless an earlier
	// tabs of the page keeps its choice under the same name and starts on a tab this one has: then that
	// tab, so tabs that follow one choice agree from the first frame, before anything is kept
	first := scope.names[0]
	if scope.persist != "" {
		for _, h := range c.tabHosts {
			if h != scope && h.persist == scope.persist && h.start != "" && containsName(scope.names, h.start) {
				first = h.start
				break
			}
		}
	}
	scope.start = first
	state := make(map[string]any, len(site.state))
	for name, v := range site.state {
		state[name] = v
	}
	state["selected"] = first
	site.state = state
	site.inputs = append(site.inputs, input{name: "selected", in: &interpolation{parts: []part{{text: first}}}, source: quoteString(first)})
	return scope, nil
}

// persistName is a name a choice is kept under, written by the author: a letter without marks (a-z),
// then such letters, digits, '-', '_' or '.' — a word that reads the same in a URL, a storage key and
// a file name.
var persistName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)

// tabsWords reads the tabs' own word off its host, which then goes on without it: persist, written
// out — "true" (the set of the tab names), "false" (the same as not writing it) or a name. It returns
// "" for none, "true", or the name. remember, the word before persist, is an error that names it.
func (c *compiler) tabsWords(n *Node) (*Node, string, error) {
	host := *n
	host.Attrs = nil
	persist := ""
	for i := range n.Attrs {
		a := &n.Attrs[i]
		base := a.Name
		if !isEvent(a.Name) {
			base = a.Name[strings.LastIndex(a.Name, ":")+1:]
		}
		if base != "persist" && base != "remember" {
			host.Attrs = append(host.Attrs, *a)
			continue
		}
		if base != a.Name {
			return nil, "", errAttr(n, a, "%s của tabs không nhận tiền tố: lựa chọn được giữ một cách trên mọi độ rộng", base)
		}
		value := strings.TrimSpace(a.Value)
		if base == "remember" {
			name := "tên"
			if persistName.MatchString(value) && !strings.EqualFold(value, "true") && !strings.EqualFold(value, "false") {
				name = value
			}
			return nil, "", errAttr(n, a, `remember đã đổi thành persist (16/10/2026): viết persist="true" để mọi tabs có cùng các tab trên site đi theo một lựa chọn, hay persist="%s" để giữ lựa chọn dưới tên này`, name)
		}
		if a.Bare || value == "" {
			return nil, "", errAttr(n, a, `persist cần một giá trị: persist="true" giữ lựa chọn theo bộ tên tab, persist="tên" giữ theo một tên chọn`)
		}
		if err := persistValue(n, a, value); err != nil {
			return nil, "", err
		}
		if value != "false" {
			persist = value
		}
	}
	return &host, persist, nil
}

// persistValue says what is wrong with a persist written, nil when it is "true", "false" or a name.
func persistValue(n *Node, a *Attr, value string) error {
	const words = `persist nhận "true" (giữ lựa chọn theo bộ tên tab: mọi tabs có cùng các tab đi theo nhau), "false" (không giữ, như không viết) hay một tên (giữ dưới tên đó, cho những bộ tab khác nhau mà nên đi cùng nhau)`
	switch lower := strings.ToLower(value); {
	case strings.Contains(value, "{{"):
		return errAttr(n, a, `persist viết sẵn, không đọc từ {{ }}: engine đặt tên giữ lựa chọn và pre-paint của nó khi biên dịch trang; %s`, words)
	case value == "true" || value == "false":
		return nil
	case lower == "true" || lower == "false":
		return errAttr(n, a, `persist="%s": viết chữ thường, persist="%s"`, value, lower)
	case lower == "yes" || lower == "on" || lower == "1":
		return errAttr(n, a, `persist="%s" không phải một giá trị của persist: muốn giữ lựa chọn thì viết persist="true"; %s`, value, words)
	case lower == "no" || lower == "off" || lower == "0":
		return errAttr(n, a, `persist="%s" không phải một giá trị của persist: không muốn giữ thì bỏ persist (hay persist="false"); %s`, value, words)
	case !persistName.MatchString(value):
		suggest := `persist="true"`
		if name := persistSuggestion(value); name != "" {
			suggest = `persist="` + name + `" (hay persist="true")`
		}
		return errAttr(n, a, `persist="%s": tên giữ lựa chọn bắt đầu bằng một chữ cái không dấu (a–z), rồi chữ không dấu, số, '-', '_', '.'; viết %s`, value, suggest)
	}
	return nil
}

// persistSuggestion is the name nearest what was written: a letter with marks loses them (lựa chọn →
// lua-chon, über → uber), what is not a letter a-z, a digit, '-', '_' or '.' becomes '-', and what
// comes before the first letter goes; "" when no letter is left.
func persistSuggestion(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.Is(unicode.Mn, r) {
			continue // a mark written apart from its letter (e, then U+0302): the letter stays
		}
		if base, ok := unmarked[unicode.ToLower(r)]; ok {
			if unicode.IsUpper(r) {
				base = unicode.ToUpper(base)
			}
			r = base
		}
		switch {
		case r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'):
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	for name != "" && !(name[0] >= 'a' && name[0] <= 'z' || name[0] >= 'A' && name[0] <= 'Z') {
		name = strings.TrimLeft(name[1:], "-._")
	}
	return name
}

// unmarked is each lowercase letter with marks of Vietnamese and the Latin alphabets near it, to the
// letter without them (đ is a letter of its own that Unicode does not decompose: d).
var unmarked = func() map[rune]rune {
	m := map[rune]rune{}
	for base, marked := range map[rune]string{
		'a': "áàảãạăắằẳẵặâấầẩẫậäåā",
		'e': "éèẻẽẹêếềểễệëē",
		'i': "íìỉĩịîïī",
		'o': "óòỏõọôốồổỗộơớờởỡợöøō",
		'u': "úùủũụưứừửữựûüū",
		'y': "ýỳỷỹỵÿ",
		'd': "đ",
		'c': "ç",
		'n': "ñ",
	} {
		for _, r := range marked {
			m[r] = base
		}
	}
	return m
}()

// tabsKey is the name a choice is kept under: for persist="true" the set of the tab names, sorted and
// joined by "+" (bash+powershell) — the same for every tabs of the site that has those tabs, in any
// order, on any page; for persist="<name>" that name.
func tabsKey(persist string, names []string) string {
	if persist != "true" {
		return persist
	}
	set := append([]string(nil), names...)
	sort.Strings(set)
	return strings.Join(set, "+")
}

// givePersist hands the instance the name its choice is kept under, as an input like selected: in its
// data-kitwork-scope for the kernel, and in its state as it starts for the server.
func givePersist(site *callSite, key string) {
	state := make(map[string]any, len(site.state)+1)
	for name, v := range site.state {
		state[name] = v
	}
	state["persist"] = key
	site.state = state
	site.inputs = append(site.inputs, input{name: "persist", in: &interpolation{parts: []part{{text: key}}}, source: quoteString(key)})
}

// pressable reports whether a person can press or type in an element as written: a control, a link,
// an accordion, or any element with an event.
func pressable(n *Node) bool {
	switch n.Tag {
	case "button", "link", "input", "select", "textarea", "accordion":
		return true
	}
	for _, a := range n.Attrs {
		if isEvent(a.Name) {
			return true
		}
	}
	return false
}

func containsName(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// tabButton makes a <button> inside a tabs host a tab when its click is select('<name>'): role="tab",
// the name as its value, aria-selected and tabindex bound to the choice. A select(…) whose name is not
// written out is no tab, and the author hears so.
func (c *compiler) tabButton(n *Node, p *attrPlan, o *elemOp, w *strings.Builder, cx ctx) error {
	var click *Attr
	for i := range p.events {
		if p.events[i].Name == "click" {
			click = &p.events[i]
		}
	}
	if click == nil {
		return nil
	}
	e, err := compileExpr(click.Value, n, click)
	if err != nil {
		return err
	}
	owner := cx.scope.declaring("select")
	if owner == nil || owner.component == nil || owner.component.name != "tabs" {
		return nil
	}
	name, ok := tabName(e.ir)
	if !ok {
		if selectCall(e.ir) {
			c.warn(n.File, click.Line, click.Column, `click="`+click.Value+`": engine nhận một nút là tab khi click là select('tên') với tên viết sẵn; nút này chọn được tab nhưng không phải tab (không có role="tab", không đi bằng phím mũi tên)`)
		}
		return nil
	}
	if cx.forAt != cx.tabs.forAt {
		return errAttr(n, click, "tab %s nằm trong for: mỗi tab viết một lần, với tên viết sẵn trong select('…')", name)
	}
	if a := p.semantic["value"]; a != nil {
		return errAttr(n, a, "value của một tab là tên của nó, engine viết từ select('%s'): bỏ value", name)
	}
	chosen := "selected == " + quoteString(name)
	for _, d := range []struct{ name, source string }{
		{"aria-selected", chosen},
		{"tabindex", chosen + " ? 0 : -1"},
	} {
		in, err := parseInterpolation("{{ "+d.source+" }}", n, click)
		if err != nil {
			return err
		}
		o.dynAttrs = append(o.dynAttrs, dynAttr{name: d.name, in: in})
	}
	w.WriteString(` role="tab"`)
	writeAttr(w, "value", name)
	cx.tabs.tabs = append(cx.tabs.tabs, &tabButton{name: name, o: o, id: p.id, node: n, click: click})
	return nil
}

// notePanel records an element whose show compares an alias's selected with a name: a panel of a tab
// when the alias is a tabs host's, which is known once the page is compiled (wireTabs). o is nil for an
// element that cannot be one.
func (c *compiler) notePanel(n *Node, p *attrPlan, o *elemOp, cx ctx) {
	if p.showExpr == nil {
		return
	}
	alias, name, ok := panelOf(p.showExpr.ir)
	if !ok {
		return
	}
	c.tabPanels = append(c.tabPanels, &tabPanel{alias: alias, name: name, aliases: cx.aliases, o: o, node: n, show: attrNamed(n, "show"), id: p.id, inFor: cx.forAt != nil, seq: c.next("tabs order")})
}

// tabsAlias is an alias where it is set: a component used twice has an alias of the same name in each
// instance's own .ui, and each is that instance's tabs.
type tabsAlias struct {
	frame *aliasFrame
	alias string
}

// setter is the frame in reach that sets an alias, the nearest going out; nil when none does.
func (f *aliasFrame) setter(alias string) *aliasFrame {
	for s := f; s != nil; s = s.parent {
		if _, ok := s.set[alias]; ok {
			return s
		}
	}
	return nil
}

// wireTabs ties each tab to its panels once every tablist and every show of the page is known: the
// tab's id and aria-controls, the panel's id, role, aria-labelledby and tabindex. A panel is its alias's
// where it stands: the page's, or its own instance's when a component holding a tabs is used twice.
func (c *compiler) wireTabs() error {
	hosts := map[tabsAlias]*tabsScope{}
	for _, h := range c.tabHosts {
		if h.alias != "" {
			hosts[tabsAlias{h.aliases, h.alias}] = h
		}
		for i, t := range h.tabs {
			t.htmlID = t.id
			if t.htmlID == "" {
				t.htmlID = fmt.Sprintf("kitwork-tab-%d-%d", h.number, i+1)
			}
		}
	}
	for _, panel := range c.tabPanels {
		h := hosts[tabsAlias{panel.aliases.setter(panel.alias), panel.alias}]
		if h == nil {
			continue // another component's selected, or an alias no host sets (told by errUse)
		}
		t, index := h.tab(panel.name)
		if t == nil {
			c.warn(panel.node.File, panel.show.Line, panel.show.Column, fmt.Sprintf("show so %s.selected với '%s', mà %s không có tab %s (%s): phần tử này không bao giờ hiện",
				panel.alias, panel.name, panel.alias, panel.name, strings.Join(h.names, ", ")))
			continue
		}
		if panel.o == nil {
			return errAttr(panel.node, panel.show, "<%s> không làm panel của tab %s được (show của nó chưa đổi theo tab ở trình duyệt): đặt show lên một khung bọc nó, <box show=\"%s\">…</box>",
				panel.node.Tag, panel.name, strings.TrimSpace(panel.show.Value))
		}
		if panel.inFor {
			return errAttr(panel.node, panel.show, "panel của tab %s nằm trong for: một tab có panel của nó, viết một lần", panel.name)
		}
		id, head := panel.id, ""
		if id == "" {
			id = fmt.Sprintf("kitwork-tabpanel-%d-%d", h.number, index+1)
			if len(t.panels) > 0 {
				id += fmt.Sprintf("-%d", len(t.panels)+1)
			}
			head = ` id="` + id + `"`
		}
		panel.o.staticAttrs = head + ` role="tabpanel" aria-labelledby="` + t.htmlID + `" tabindex="0"` + panel.o.staticAttrs
		t.panels = append(t.panels, id)
		t.wired = append(t.wired, panel)
	}
	for _, h := range c.tabHosts {
		for _, t := range h.tabs {
			if len(t.panels) == 0 {
				if h.alias != "" {
					c.warn(t.node.File, t.click.Line, t.click.Column, fmt.Sprintf(`tab %s của %s không có panel: viết show="%s.selected == '%s'" trên phần tử nó cho hiện`, t.name, h.alias, h.alias, t.name))
				}
				continue
			}
			head := ""
			if t.id == "" {
				head = ` id="` + t.htmlID + `"`
			}
			t.o.staticAttrs = head + strings.Replace(t.o.staticAttrs, ` role="tab"`, ` role="tab" aria-controls="`+strings.Join(t.panels, " ")+`"`, 1)
		}
		if h.alias == "" {
			at := attrNamed(h.node, "component")
			c.warn(h.node.File, at.Line, at.Column, `tabs không có as: ngoài thẻ chủ không gì đọc được tab đang chọn; đặt as="$tên" trên thẻ chủ rồi viết show="$tên.selected == 'tên tab'" trên panel của mỗi tab`)
		}
	}
	c.tabsPrepaints()
	return nil
}

// tabsPrepaints writes, for each name the page's tabs persist under, its pre-paint right after the
// element of those tabs that starts last (a tablist or a panel): every tab and panel that follows the
// name is in the document when it runs, and the parser has not handed the page to a frame since. It
// carries the name and the tabs that have panels, by id; its body is one constant.
func (c *compiler) tabsPrepaints() {
	var keys []string
	members := map[string][]*tabsScope{}
	for _, h := range c.tabHosts {
		if h.persist == "" || h.o == nil {
			continue
		}
		if members[h.persist] == nil {
			keys = append(keys, h.persist)
		}
		members[h.persist] = append(members[h.persist], h)
	}
	for _, key := range keys {
		var ids []string
		last, at := (*elemOp)(nil), -1
		for _, h := range members[key] {
			if h.seq > at {
				last, at = h.o, h.seq
			}
			for _, t := range h.tabs {
				if len(t.wired) > 0 {
					ids = append(ids, t.htmlID)
				}
				for _, panel := range t.wired {
					if panel.seq > at {
						last, at = panel.o, panel.seq
					}
				}
			}
		}
		if len(ids) == 0 {
			continue
		}
		var b strings.Builder
		b.WriteString("<script " + tabsPrepaintMarker)
		writeAttr(&b, "data-kitwork-persist", key)
		writeAttr(&b, "data-kitwork-tabs", strings.Join(ids, " "))
		b.WriteString(">" + tabsPrepaint + "</script>")
		last.after += b.String()
	}
}

// tabsPrepaint is the body of the pre-paint of the tabs that persist under one name. It runs where the
// parser meets it, before the first frame and before any of Kitwork's deferred scripts. It reads what
// the kernel's tabs keeps through kit.storage in a browser — localStorage "kitwork:tabs:<name>", the
// tab's name as JSON — and, on every tablist of the name that has a tab of that name, shows that tab's
// panels, hides the others, and moves aria-selected and tabindex to it. A name kept that is no tab,
// a value that is no name, storage that refuses, anything that throws: nothing changes, the page stays
// as the server painted it. The kernel then starts each tabs from the same choice, so nothing moves
// again. In a desktop or mobile app kit.storage is the native bridge, not localStorage: nothing is
// found here, and the kernel restores when the store answers. The name and the tabs' ids are read off
// the script element itself, so the body is one constant (TabsPrepaintHash).
const tabsPrepaint = `(function(s){try{var k=JSON.parse(localStorage.getItem("kitwork:tabs:"+s.getAttribute("data-kitwork-persist")));if(typeof k!="string"||!k)return;` +
	`var t=s.getAttribute("data-kitwork-tabs").split(" ").map(function(i){return document.getElementById(i)}).filter(Boolean),` +
	`h=t.filter(function(b){return b.value===k}).map(function(b){return b.closest('[role="tablist"]')});` +
	`t.forEach(function(b){if(h.indexOf(b.closest('[role="tablist"]'))<0)return;var o=b.value===k;` +
	`b.setAttribute("aria-selected",o);b.setAttribute("tabindex",o?0:-1);` +
	`(b.getAttribute("aria-controls")||"").split(" ").forEach(function(i){var p=document.getElementById(i);if(p)p.hidden=!o})})` +
	`}catch(e){}})(document.currentScript)`

// tabsPrepaintMarker is the attribute a tabs pre-paint carries: how a response knows it holds one.
const tabsPrepaintMarker = `data-kitwork-generated="tabs"`

// CarriesTabsPrepaint reports whether a page the engine wrote holds a tabs pre-paint.
func CarriesTabsPrepaint(html string) bool {
	return strings.Contains(html, tabsPrepaintMarker)
}

// TabsPrepaintHash is the CSP source expression of the tabs pre-paint ('sha256-…'): the same for every
// page and every group, so a Content-Security-Policy can allow exactly that one script. The engine adds
// it to a policy the site sends, as it does the appearance pre-paint's (work/theme_csp.go).
func TabsPrepaintHash() string {
	sum := sha256.Sum256([]byte(tabsPrepaint))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// tabName reads a click that chooses a tab, select('<name>'), into the name: one string written out,
// not empty.
func tabName(ir any) (string, bool) {
	if !selectCall(ir) {
		return "", false
	}
	args, ok := ir.([]any)[2].([]any)
	if !ok || len(args) != 1 {
		return "", false
	}
	return stringLiteral(args[0])
}

// selectCall reports whether an expression is a bare call of select.
func selectCall(ir any) bool {
	node, ok := ir.([]any)
	if !ok || len(node) != 3 || node[0] != "call" {
		return false
	}
	callee, ok := node[1].([]any)
	return ok && len(callee) == 2 && callee[0] == "$" && callee[1] == "select"
}

// panelOf reads a show that compares an alias's selected with a name — $files.selected == 'router',
// with === too, either side — into the alias and the name.
func panelOf(ir any) (alias, name string, ok bool) {
	node, isNode := ir.([]any)
	if !isNode || len(node) != 3 || node[0] != "==" && node[0] != "===" {
		return "", "", false
	}
	for _, sides := range [][2]any{{node[1], node[2]}, {node[2], node[1]}} {
		if alias, ok = aliasSelected(sides[0]); !ok {
			continue
		}
		if name, ok = stringLiteral(sides[1]); ok {
			return alias, name, true
		}
	}
	return "", "", false
}

// aliasSelected reads $alias.selected into the alias.
func aliasSelected(ir any) (string, bool) {
	node, ok := ir.([]any)
	if !ok || len(node) != 3 || node[0] != "." || node[2] != "selected" {
		return "", false
	}
	ref, ok := node[1].([]any)
	if !ok || len(ref) != 2 || ref[0] != "$" {
		return "", false
	}
	alias, _ := ref[1].(string)
	return alias, aliasName(alias)
}

// stringLiteral reads a string written out, not empty.
func stringLiteral(ir any) (string, bool) {
	node, ok := ir.([]any)
	if !ok || len(node) != 2 || node[0] != "#" {
		return "", false
	}
	s, ok := node[1].(string)
	return s, ok && s != ""
}
