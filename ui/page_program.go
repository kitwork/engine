package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/kitwork/engine/jit/hydrate"
)

// The program of a page that runs in the kernel (28/10/2026, docs/KITWORK_BRIDGE.md, lát 1 of the compiled
// page). The page's elements carry no expression: each element whose values follow the page's data carries
// one short mark, data-kitwork-mark="n", and the page's program, a file of its own (/_kitwork/page.<hash>.js), says for
// mark n what to write, from which expression (its IR, compiled here), and which names of the page it reads.
// In the browser a name that changes runs again only what reads it (call.js), where the kernel's paint ran
// every binding of the page on every write: at 1.000 rows a keystroke cost 14 ms, and the expressions written
// on every row were half of the page's HTML.
//
//	<input value="{{ title }}" />                 →  <input data-kitwork-mark="0" value="">        g[0]: value: title, reads title
//	<item for="todo of todos; key: todo.id">      →  rows marked data-kitwork-item/key, the template data-kitwork-template="0"
//	  <text>{{ todo.title }}</text>              →  <p data-kitwork-mark="3">…</p>                g[3]: text: todo.title, row of list 0
//
// A page with a component of Kitwork in it (a dropdown, a countdown…) is not compiled this way yet: what is
// inside the component is the kernel's, and the page is written as before (runtime.go), the whole page.

// pageProgram is a page's program as it is written to its file.
type pageProgram struct {
	Groups []programGroup `json:"g"`
	Lists  []programList  `json:"l"`
}

// programGroup is what one marked element writes and listens to.
type programGroup struct {
	// List is the list whose row the element stands in, -1 outside every row
	List    int           `json:"l"`
	Binds   []programPair `json:"b,omitempty"`
	Classes []programPair `json:"c,omitempty"`
	Events  []programPair `json:"e,omitempty"`
}

// programPair is one value an element writes (a binding: text, show, value, disabled…), one class it turns on
// and off, or one event it listens to, with the expression and the names of the page it reads.
type programPair struct {
	Name  string   `json:"n"`
	IR    any      `json:"x"`
	Reads []string `json:"r,omitempty"`
}

// programList is one list of the page drawn by key: its item and index names, the expression of the list
// (a name of the page), its key and the names it reads.
type programList struct {
	Item  string   `json:"i"`
	Index string   `json:"x,omitempty"`
	List  any      `json:"a"`
	Key   any      `json:"k,omitempty"`
	Reads []string `json:"r,omitempty"`
}

// liveListRef is the list a row frame is the rows of, on a page with a program.
type liveListRef struct {
	index int
}

// programModifiers are the modifiers of an event the program runs; an event with any other keeps the page
// in the kernel (compileFallback).
var programModifiers = map[string]bool{"prevent": true, "stop": true}

// addList records a live list of the page and returns its reference.
func (p *pageProgram) addList(each *forSpec, page *frame) *liveListRef {
	list := programList{Item: each.item, Index: each.index, List: each.list.ir, Reads: pageReads(each.list.ir, page)}
	if each.key != nil {
		list.Key = each.key.ir
	}
	p.Lists = append(p.Lists, list)
	return &liveListRef{index: len(p.Lists) - 1}
}

// group turns the runtime pairs of an element into a group of the program and returns its mark; false when
// the program cannot run them (the page then falls back, c.programOff).
func (c *compiler) programGroup(scope *frame, pairs runtimePairs) (string, bool) {
	group := programGroup{List: -1}
	for s := scope; s != nil; s = s.parent {
		if s.list != nil {
			group.List = s.list.index
			break
		}
	}
	read := func(list []string, events bool) ([]programPair, bool) {
		var out []programPair
		for _, pair := range list {
			name, source, ok := strings.Cut(pair, ": ")
			if !ok {
				return nil, false
			}
			if events {
				_, modifiers, _ := strings.Cut(name, ".")
				for _, m := range splitModifiers(modifiers) {
					if m != "" && !programModifiers[m] {
						return nil, false
					}
				}
			}
			ir, err := hydrate.Compile(source)
			if err != nil {
				return nil, false
			}
			out = append(out, programPair{Name: name, IR: ir, Reads: pageReads(ir, c.page)})
		}
		return out, true
	}
	var ok bool
	if group.Binds, ok = read(pairs.binds, false); !ok {
		return "", false
	}
	if group.Classes, ok = read(pairs.classes, false); !ok {
		return "", false
	}
	if group.Events, ok = read(pairs.events, true); !ok {
		return "", false
	}
	c.program.Groups = append(c.program.Groups, group)
	return ` data-kitwork-mark="` + strconv.Itoa(len(c.program.Groups)-1) + `"`, true
}

// pageReads are the names of the page an expression reads: what makes it run again when they change.
func pageReads(ir any, page *frame) []string {
	var out []string
	for _, name := range usedNames(ir) {
		if page != nil && page.names[name] {
			out = append(out, name)
		}
	}
	return out
}

// build writes the program's file and its id: the file registers the program under its id, which the page's
// data block names (writeState), so a page Drive comes back to finds its program without the file running
// again.
func (p *pageProgram) build() (script []byte, id string) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, ""
	}
	sum := sha256.Sum256(data)
	id = hex.EncodeToString(sum[:])[:16]
	var b strings.Builder
	b.WriteString("(function(k){(k.pages=k.pages||{})[\"")
	b.WriteString(id)
	b.WriteString("\"]=")
	b.Write(data)
	b.WriteString(";if(k.pageReady)k.pageReady(\"")
	b.WriteString(id)
	b.WriteString("\")})(window.kitwork=window.kitwork||{});\n")
	return []byte(b.String()), id
}

// Script is the file of the page's program, nil for a page without one: /_kitwork/page.<hash>.js.
func (p *Page) Script() []byte {
	if p == nil || p.live == nil {
		return nil
	}
	return p.live.script
}
