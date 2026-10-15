package ui

import "errors"

// The compiler speaks in three kinds (decision D, 12/10/2026):
//
//   - an ERROR stops the compile: something required is missing (a link's value, an image's value), a
//     word the compiler cannot understand (an unknown tag, attribute or value: a typo never vanishes
//     silently), safety (a javascript: link), or structure the output cannot be built from;
//   - a WARNING lets the page render as written and tells the author, while developing, of what works
//     but is probably not what they want or hurts some readers: a pair of colours short of WCAG's
//     contrast (contrast.go), widths joined by a comma (classes.go);
//   - a plain design choice is SILENT.
//
// A page compiles to its HTML and its warnings (Page.Warnings), each once, in the order the compiler
// found them; the pairs short of contrast that one background causes are one warning at it
// (contrast.go). Where the author hears of them is the engine's to decide: while developing, the
// terminal once per generation (each Head once) and the browser's console on every load of the page;
// in production, nowhere.

// Warning is one warning of a page: where it stands (file:line:col of the attribute or the element
// that decided it) and what to write instead.
type Warning struct {
	File    string
	Line    int
	Column  int
	Message string
	// held is a pair short of contrast that waits in the page's list, in the order found, for the
	// compile's end (contrast.go, tellContrast): the pairs that fall short on one background are told
	// there together, once, at that background. Every warning a page gives has none.
	held *heldPair
	// head is what the line of a group of pairs (contrast.go, groupLine) says of its cause, the same on
	// every page that shares the background; "" for every other warning (Head)
	head string
}

// Head is what a warning says of its cause, the same on every page it is said on: for the line of the
// pairs short of contrast on one background (contrast.go), file:line:col of that background, its value,
// the mode and the condition — the places and their count are the page's own; for any other warning,
// the whole line. Where warnings of many pages meet (the terminal), each head is said once.
func (w Warning) Head() string {
	if w.head != "" {
		return w.head
	}
	return w.String()
}

// String is the warning on one line, file:line:col: message, written as an error is. It carries no
// "cảnh báo:": each place that shows a warning says that itself, once.
func (w Warning) String() string {
	return (&Error{File: w.File, Line: w.Line, Column: w.Column, Message: w.Message}).Error()
}

// Warnings are the warnings the page's compile found, in the order it found them, each once. A page
// with none has nothing to tell.
func (p *Page) Warnings() []Warning {
	return append([]Warning(nil), p.warnings...)
}

// warn records a warning of the page being compiled, once: the page's set of warnings given
// (Page.warned) is the one its renders name a value from data in, so the page never says one line
// twice, whichever found it.
func (c *compiler) warn(file string, line, column int, message string) {
	w := Warning{File: file, Line: line, Column: column, Message: message}
	if c.out.warned.first(w.String()) {
		c.out.warnings = append(c.out.warnings, w)
	}
}

// The contrast check's two modes, router.contrast: how a pair of colours short of WCAG 2.2 AA is told.
const (
	// ContrastWarning (the default, also "") makes each pair a warning: the page renders as written.
	ContrastWarning = "warning"
	// ContrastStrict makes each pair a compile error: the page answers it instead of rendering.
	ContrastStrict = "strict"
)

// ContrastModeError is what a mode the check does not have answers, router.contrast and Site.Contrast
// alike: the two words it takes.
func ContrastModeError(mode string) error {
	return errors.New(`router.contrast("` + mode + `"): chỉ nhận "warning" (mặc định: cặp màu thiếu tương phản là cảnh báo, trang vẫn hiện như đã viết) hoặc "strict" (cặp đó là lỗi biên dịch)`)
}

// strictContrast reports whether the route's pairs short of contrast are compile errors.
func (s *Site) strictContrast() bool { return s.Contrast == ContrastStrict }
