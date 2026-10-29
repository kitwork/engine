package ui

import (
	"encoding/json"
	"sort"
	"strings"
)

// The page's data block (29/10/2026, docs/KITWORK_BRIDGE.md mục 3): one, for the page and for every component of
// the site the browser runs, written once at the end of <content /> so Drive brings the new page's with it. The page
// is an instance like any other, the root one: each instance's data stands under its place, the page's under
// "page", a component's under the place it is called from (callPlace); which component each place is; the jobs
// and the vars of each component, the page being the component "page"; and the bridge's own bookkeeping apart:
//
//	{ "page":   { "title": "", "todos": [ … ], "left": 3 },
//	  "c3fq8w": { "items": [], "done": false, "receipt": "", "shippingFee": 20000 },
//	  "components": { "c3fq8w": "shop-cart" },
//	  "jobs":   { "page": ["add", "remove", "toggle"], "shop-cart": ["add", "checkout"] },
//	  "vars":   { "page": ["title"], "shop-cart": ["done", "items", "receipt"] },
//	  "bridge": { "program": "95e5…", "keys": { "page": { "todos": "id" } }, "defaults": { "shop-cart": { … } },
//	              "revs": { "page": { … }, "c3fq8w": { … } }, "stamp": 1793240775002337, "tickets": [ … ] } }
//
// A component's jobs and vars are its own, whatever place it stands at, and so are the values its source writes
// out (bridge.defaults), which the kernel starts each instance of it from before the instance's own: a component
// the page calls after its content (a footer's) is registered too, though its data comes after the block. A place
// is "c" and at most seven letters and digits (a 32-bit hash in base 36), so it is none of the block's own keys.

// pagePlace is the place of the page among the instances of the bridge: the root one.
const pagePlace = "page"

// drawnInstance is an instance of a component the browser runs, as one render of the page drew it: the
// component it is, the data of its bind the tab holds (its vars among them) and the revision of each name of
// that data that is not a var.
type drawnInstance struct {
	component string
	data      map[string]any
	revs      map[string]string
}

// keepInstance notes, as the host of an instance the browser runs is drawn, its data and the revision of each
// name of it the tab holds that is the server's (29/10/2026): its consts and inputs, not its vars, which are the
// tab's, nor its jobs. A call of one of its jobs sends the revisions back, and a name the tab holds at another
// revision than the run before the job comes whole (work/component_call.go), as a page's does.
func (o *elemOp) keepInstance(r *run, own map[string]any) {
	def := o.instanceOf
	data, revs := map[string]any{}, map[string]string{}
	for name, v := range own {
		if _, bound := def.state[name]; !bound || def.jobs[name] {
			continue
		}
		if _, err := json.Marshal(v); err != nil {
			continue // a value JSON cannot carry: the kernel reads it as nothing
		}
		data[name] = v
		if def.vars[name] {
			continue
		}
		if rev := Revision(v); rev != "" {
			revs[name] = rev
		}
	}
	if r.drawn == nil {
		r.drawn = map[string]drawnInstance{}
	}
	r.drawn[o.at] = drawnInstance{component: def.name, data: data, revs: revs}
}

// writePageBlock writes the block. page is the data of the page's bind, nil on the skeleton, which writes every
// name of it with no value (for the head to link the bridge and for the check of the names the page reads);
// drawn are the instances the render drew before the block.
func writePageBlock(b *strings.Builder, live *livePage, components *liveComponents, page map[string]any, drawn map[string]drawnInstance,
	revs map[string]string, stamp int64, tickets []string) {
	if live == nil && (components == nil || len(components.defs) == 0) {
		return
	}
	block := map[string]any{}
	jobs, vars, bridge, held := map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}
	if live != nil {
		state := make(map[string]any, len(live.names))
		for _, name := range live.names {
			if page == nil {
				state[name] = nil
				continue
			}
			v, ok := page[name]
			if !ok {
				continue
			}
			if _, err := json.Marshal(v); err == nil {
				state[name] = v
			}
		}
		block[pagePlace] = state
		jobs[pagePlace], vars[pagePlace] = emptyList(live.jobs), emptyList(live.vars)
		if live.program != "" {
			bridge["program"] = live.program
		}
		keys := map[string]string{}
		for name, key := range live.keys {
			if key != "" {
				keys[name] = key
			}
		}
		if len(keys) > 0 {
			bridge["keys"] = map[string]any{pagePlace: keys}
		}
		if len(revs) > 0 {
			// the revision of each name of its data, a var's aside: what its calls send back (work/view_patch.go)
			isVar := map[string]bool{}
			for _, name := range live.vars {
				isVar[name] = true
			}
			mine := map[string]string{}
			for _, name := range live.names {
				if rev, ok := revs[name]; ok && !isVar[name] {
					mine[name] = rev
				}
			}
			held[pagePlace] = mine
			if stamp > 0 {
				bridge["stamp"] = stamp // the moment it was read: the page takes no data read before (work/view_call.go)
			}
			if len(tickets) > 0 {
				bridge["tickets"] = tickets // what its channel holds to be told of changes (work/view_changes.go)
			}
		}
	}
	if components != nil && len(components.defs) > 0 {
		defaults := map[string]any{}
		for name, def := range components.defs {
			state := map[string]any{}
			own, mutable := []string{}, []string{}
			for key, v := range def.state {
				if def.jobs[key] {
					own = append(own, key)
					continue
				}
				state[key] = v
				if def.vars[key] {
					mutable = append(mutable, key)
				}
			}
			sort.Strings(own)
			sort.Strings(mutable)
			jobs[name], vars[name], defaults[name] = own, mutable, state
		}
		bridge["defaults"] = defaults
		places := map[string]string{}
		for at, instance := range drawn {
			if components.defs[instance.component] == nil {
				continue
			}
			block[at], places[at] = instance.data, instance.component
			if len(instance.revs) > 0 {
				held[at] = instance.revs
			}
		}
		if len(places) > 0 {
			block["components"] = places
		}
	}
	block["jobs"], block["vars"] = jobs, vars
	if len(held) > 0 {
		bridge["revs"] = held
	}
	if len(bridge) > 0 {
		block["bridge"] = bridge
	}
	data, err := json.Marshal(block)
	if err != nil {
		return
	}
	b.WriteString(`<script type="application/json" data-kitwork-page>`)
	b.Write(data) // json.Marshal writes <, > and & as unicode escapes: nothing here closes the script
	b.WriteString(`</script>`)
}

func emptyList(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}
