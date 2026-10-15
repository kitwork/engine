package ui

import (
	"html"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// The presentation skeleton of a page: every class, icon, component host and runtime attribute the
// page can ever put in its HTML, written once when the page is compiled. The engine's presentation
// passes (icons, logos, scripts, the appearance pre-paint) run over it once — not over every
// response: the icons they find are one stylesheet of the page, and the rest of the <head> they
// produce is reused by every request (KITWORK_UI §18: the HTML is not rescanned on each response).
// The page's own classes need no pass: their rules were recorded as the page compiled (Page.rules).
// What only a request's data can say (an icon named by the data) is recorded while the page renders
// and finished separately (Page.Document, Request.IconsHead).

// skeletonState is computed once per compiled page.
type skeletonState struct {
	once sync.Once
	html string
	// classes is every class the skeleton writes: the static ones, those of the parts compiled to
	// plain HTML, and every value a dynamic class attribute can yield, read off its expression.
	classes map[string]bool
}

// skeletonClassRe reads the class attributes the skeleton writes (writeAttr: a space, then the name;
// never data-kitwork-class, whose keys are toggled, not written).
var skeletonClassRe = regexp.MustCompile(` class="([^"]*)"`)

// Skeleton returns the page body with every branch present: both sides of an if, a for's row once,
// the empty part of a list, a shown and a hidden element alike, and each dynamic class as every value
// the compiler can read off its expression (the literals a ternary, || or ?? can yield). Text and
// attribute values from data are left out: they carry no class.
func (p *Page) Skeleton() string {
	p.prepareSkeleton()
	return p.skeleton.html
}

func (p *Page) prepareSkeleton() {
	p.skeleton.once.Do(func() {
		var b strings.Builder
		writeSkeleton(&b, p.root)
		p.skeleton.html = b.String()
		classes := map[string]bool{}
		for _, m := range skeletonClassRe.FindAllStringSubmatch(p.skeleton.html, -1) {
			for _, c := range strings.Fields(html.UnescapeString(m[1])) {
				classes[c] = true
			}
		}
		p.skeleton.classes = classes
	})
}

// covered reports whether the skeleton already lists class, written anywhere on the page or yielded
// by an expression: an icon it lists is in the page's icons stylesheet, so a request whose data names
// it adds no CSS of its own.
func (p *Page) covered(class string) bool {
	p.prepareSkeleton()
	return p.skeleton.classes[class]
}

func writeSkeleton(b *strings.Builder, o op) {
	switch o := o.(type) {
	case *staticOp:
		b.WriteString(o.s)
	case *elemOp:
		if o.fragment {
			for _, ch := range o.children {
				writeSkeleton(b, ch)
			}
			return
		}
		b.WriteByte('<')
		b.WriteString(o.tag)
		classes := append(append([]string(nil), o.staticClass...), o.candidateClasses()...)
		if len(classes) > 0 {
			writeAttr(b, "class", strings.Join(classes, " "))
		}
		b.WriteString(o.staticAttrs)
		b.WriteByte('>')
		if o.void {
			return
		}
		for _, ch := range o.children {
			writeSkeleton(b, ch)
		}
		for _, ch := range o.emptyOps {
			writeSkeleton(b, ch)
		}
		b.WriteString("</")
		b.WriteString(o.tag)
		b.WriteByte('>')
	}
	// textOp: text carries no class
}

// candidateClasses are the classes this element's dynamic class attributes can yield, as far as their
// expressions spell them out. A value the expression only reads from data yields none here.
func (o *elemOp) candidateClasses() []string {
	var out []string
	for _, d := range o.dynClasses {
		for _, value := range interpolationCandidates(d.in) {
			if class, ok := d.classOf(value); ok && class != "" {
				out = append(out, class)
			}
		}
	}
	return out
}

// maxCandidates bounds the values one attribute is expanded into: a value built from several
// expressions multiplies their literals, and the skeleton is not the place to enumerate a product.
const maxCandidates = 64

// interpolationCandidates lists every text an interpolation can render when each of its expressions
// yields one of the literals it spells out; nil when any expression yields a value from data.
func interpolationCandidates(in *interpolation) []string {
	if in == nil {
		return nil
	}
	values := []string{""}
	for _, p := range in.parts {
		if p.e == nil {
			for i := range values {
				values[i] += p.text
			}
			continue
		}
		literals, ok := resultLiterals(p.e.ir)
		if !ok {
			return nil
		}
		var next []string
		for _, v := range values {
			for _, l := range literals {
				next = append(next, v+l)
			}
		}
		if len(next) > maxCandidates {
			return nil
		}
		values = next
	}
	sort.Strings(values)
	return dedupe(values)
}

// resultLiterals reads the values an expression can yield when they are written in it: a literal, the
// two arms of a ternary, the two sides of || and ??, the right side of &&. Anything that reads a name
// or calls a function is not known before the page renders.
func resultLiterals(ir any) ([]string, bool) {
	node, ok := ir.([]any)
	if !ok || len(node) == 0 {
		return nil, false
	}
	op, _ := node[0].(string)
	switch op {
	case "#":
		if len(node) < 2 {
			return nil, false
		}
		return []string{stringify(node[1])}, true
	case "?":
		if len(node) < 4 {
			return nil, false
		}
		return unionLiterals(node[2], node[3])
	case "||", "??":
		if len(node) < 3 {
			return nil, false
		}
		return unionLiterals(node[1], node[2])
	case "&&":
		if len(node) < 3 {
			return nil, false
		}
		// a falsy left side renders as no class; the class comes from the right
		return resultLiterals(node[2])
	}
	return nil, false
}

func unionLiterals(a, b any) ([]string, bool) {
	left, ok := resultLiterals(a)
	if !ok {
		return nil, false
	}
	right, ok := resultLiterals(b)
	if !ok {
		return nil, false
	}
	return append(left, right...), true
}
