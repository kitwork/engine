package ui

import (
	"encoding/json"
	"html"
	"sort"
	"strconv"
	"strings"
)

// A page that runs in the kernel (28/10/2026, docs/KITWORK_BRIDGE.md). The page of a router.view whose
// bind holds a job or a var is one component, the way the spec has always drawn it (KITWORK_UI §15, the
// component "page"): what it reads of its bind is written as the kernel's attributes (runtime.go), its
// lists as the marker pair the kernel adopts (render.go renderLive), and the data it reads once, as JSON,
// at the end of <content /> (writeState), for the kernel to repaint from. A job of the page then sends
// back the data it changed and never the page (work/view_call.go, jit/hydrate/call.js).
//
//	router.view((context) => { var title = ""; const todos = …; return context.bind({ title, todos, add, remove }); })
//	<content>…<script type="application/json" data-kitwork-page>{"jobs":["add","remove"],"state":{"title":"","todos":[…]},"vars":["title"]}</script></content>
//
// A page whose bind holds neither stays as the server prints it: no kernel, no data written twice.

// livePage is what a page that runs in the kernel carries beyond its HTML.
type livePage struct {
	// names are the names of its bind that are data (state), jobs those that are jobs, vars those that
	// are vars: what the kernel's page scope holds
	names, jobs, vars []string
	// keys is the key of each list of the bind the page draws by key, as a path into a row ("id"): the
	// server sends that list's change by row (work/view_patch.go)
	keys map[string]string
	// script is the file of the page's program and program its id, when it has one (page_program.go)
	script  []byte
	program string
}

// startLivePage makes the page a component the kernel runs when its router.view binds a job or a var and
// the compiler knows every name of that bind: the frame every expression of the page resolves through.
func (c *compiler) startLivePage(kind string) {
	g := c.given
	if kind != pageContent || !g.view || g.names == nil || len(g.jobs) == 0 && len(g.vars) == 0 {
		return
	}
	c.page = &frame{what: "trang", names: g.names, client: true}
	c.listKeys = map[string]string{}
	live := &livePage{}
	for name := range g.names {
		switch {
		case g.jobs[name]:
			live.jobs = append(live.jobs, name)
		default:
			live.names = append(live.names, name)
			if g.vars[name] {
				live.vars = append(live.vars, name)
			}
		}
	}
	sort.Strings(live.names)
	sort.Strings(live.jobs)
	sort.Strings(live.vars)
	c.out.live = live
}

// noteLiveList records the key of a list of the bind the page draws by key (for="todo of todos; key:
// todo.id"): a list drawn twice with two keys has none.
func (c *compiler) noteLiveList(each *forSpec) {
	if c.page == nil || each.list == nil {
		return
	}
	name, ok := bareName(each.list.ir)
	if !ok || !c.page.names[name] {
		return
	}
	key := ""
	if each.key != nil {
		key, _ = keyPath(each.key.ir, each.item)
	}
	if was, seen := c.listKeys[name]; seen && was != key {
		key = ""
	}
	c.listKeys[name] = key
}

// bareName is the name an expression is, when it is one name and nothing else (todos).
func bareName(ir any) (string, bool) {
	parts, ok := ir.([]any)
	if !ok || len(parts) != 2 {
		return "", false
	}
	if op, _ := parts[0].(string); op != "$" {
		return "", false
	}
	name, ok := parts[1].(string)
	return name, ok
}

// keyPath is the path of a key that reads a field of the row (todo.id → "id", todo.author.id →
// "author.id"), false for any other key.
func keyPath(ir any, item string) (string, bool) {
	var path []string
	for {
		parts, ok := ir.([]any)
		if !ok || len(parts) < 2 {
			return "", false
		}
		switch op, _ := parts[0].(string); op {
		case ".":
			if len(parts) != 3 {
				return "", false
			}
			field, ok := parts[2].(string)
			if !ok {
				return "", false
			}
			path = append([]string{field}, path...)
			ir = parts[1]
		case "$":
			if name, _ := parts[1].(string); name != item || len(path) == 0 {
				return "", false
			}
			return strings.Join(path, "."), true
		default:
			return "", false
		}
	}
}

// ListKeys is the key of each list of the page's bind it draws by key, as a path into a row; nil for a
// page that does not run in the kernel.
func (p *Page) ListKeys() map[string]string {
	if p == nil || p.live == nil {
		return nil
	}
	return p.live.keys
}

// Live reports whether the page runs in the kernel: its router.view binds a job or a var.
func (p *Page) Live() bool {
	return p != nil && p.live != nil
}

// writeState writes the data of the page's bind the kernel repaints from, the jobs it calls and which
// names are vars. A value JSON cannot carry is left out, as the kernel would read it: nothing.
func (l *livePage) writeState(b *strings.Builder, scope map[string]any, revs map[string]string, stamp int64, tickets []string) {
	state := make(map[string]any, len(l.names))
	for _, name := range l.names {
		v, ok := scope[name]
		if !ok {
			continue
		}
		if _, err := json.Marshal(v); err == nil {
			state[name] = v
		}
	}
	block := map[string]any{"state": state, "jobs": emptyList(l.jobs), "vars": emptyList(l.vars)}
	if l.program != "" {
		block["program"] = l.program
	}
	if len(revs) > 0 {
		// the revision of each name of its data, a var's aside: what its calls send back (work/view_patch.go)
		vars := map[string]bool{}
		for _, name := range l.vars {
			vars[name] = true
		}
		held := map[string]string{}
		for _, name := range l.names {
			if rev, ok := revs[name]; ok && !vars[name] {
				held[name] = rev
			}
		}
		block["revs"] = held
		if stamp > 0 {
			block["stamp"] = stamp // the moment it was read: the page takes no data read before (work/view_call.go)
		}
		if len(tickets) > 0 {
			block["tickets"] = tickets // what its channel holds to be told of changes (work/view_changes.go)
		}
	}
	data, err := json.Marshal(block)
	if err != nil {
		return
	}
	b.WriteString(`<script type="application/json" data-kitwork-page>`)
	b.Write(data) // json.Marshal writes <, > and & as unicode escapes: nothing here closes the script
	b.WriteString(`</script>`)
}

// writeNames writes the page's data block with every name and no value: what the skeleton carries, for the
// head to link the bridge and for the check of the names the page reads to know its page scope.
func (l *livePage) writeNames(b *strings.Builder) {
	state := make(map[string]any, len(l.names))
	for _, name := range l.names {
		state[name] = nil
	}
	data, err := json.Marshal(map[string]any{"state": state, "jobs": emptyList(l.jobs), "vars": emptyList(l.vars)})
	if err != nil {
		return
	}
	b.WriteString(`<script type="application/json" data-kitwork-page>`)
	b.Write(data)
	b.WriteString(`</script>`)
}

func emptyList(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// forText is a for as the kernel reads it (data-kitwork-for): "todo of todos; key: todo.id".
func forText(each *forSpec) string {
	head := each.item
	if each.index != "" {
		head += ", " + each.index
	}
	text := head + " of " + strings.TrimSpace(each.list.source)
	if each.key != nil {
		text += "; key: " + strings.TrimSpace(each.key.source)
	}
	return text
}

// renderLive draws a list of a page that runs in the kernel as the kernel adopts it (jit/hydrate
// collectFor): the start marker naming the list, the row as a template with no item, hidden, then each
// row with the list's id and its key, then the end marker. The template counts as no item of the list.
func (o *elemOp) renderLive(r *run, scope map[string]any) {
	r.lists++
	id := "s" + strconv.Itoa(r.lists)
	list, _ := o.forEach.list.eval(scope).([]any)
	item, index := o.forEach.item, o.forEach.index
	oldItem, hadItem := scope[item]
	oldIndex, hadIndex := scope[index]
	defer func() {
		restore(scope, item, oldItem, hadItem)
		if index != "" {
			restore(scope, index, oldIndex, hadIndex)
		}
	}()
	r.b.WriteString("<!--kitwork-for:start id=" + id + "-->")
	scope[item] = nil
	if index != "" {
		scope[index] = float64(0)
	}
	items := r.items
	r.mark = ` data-kitwork-for="` + html.EscapeString(forText(o.forEach)) + `" data-kitwork-template`
	if o.liveList != nil {
		// a page with a program: the template is the program's list, which the kernel leaves alone
		r.mark = ` data-kitwork-template="` + strconv.Itoa(o.liveList.index) + `"`
	}
	r.markHidden = true
	o.renderOne(r, scope)
	r.items = items
	for i, row := range list {
		scope[item] = row
		if index != "" {
			scope[index] = float64(i)
		}
		key := strconv.Itoa(i)
		if o.forEach.key != nil {
			key = stringify(o.forEach.key.eval(scope))
		}
		r.mark = ` data-kitwork-item="` + id + `" data-kitwork-key="` + html.EscapeString(key) + `"`
		o.renderOne(r, scope)
	}
	r.mark, r.markHidden = "", false
	r.b.WriteString("<!--kitwork-for:end-->")
}
