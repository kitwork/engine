package stylesheet

import (
	"sort"
	"strconv"
	"strings"
)

// The look of <editor> (decisions of 20/10/2026, ui/editor.go): the author draws the toolbars, the
// link's popover and the writing area; the engine writes the parts it adds and this scope gives them
// their look, in page.<hash>.css only on a page that holds an editor (Rules.Editor), in the elements
// layer beside the richtext scope, which the writing area wears too (writing looks like reading).
//
//   - the host (class editor) is the box its hidden field is placed against;
//   - the writing area (editor-area) is a block of text at least rows lines high (editor-rows-N, 4
//     lines when rows is not written), its placeholder drawn from aria-placeholder while it is empty,
//     its words at least 16px on a touch screen so a phone does not zoom in (!important: a size the
//     author writes on <content>, size="small", is a class of the page layer, which outranks this one);
//   - the field the form posts (editor-field, a <textarea>) stays a real control, so the browser's own
//     check of required still holds: drawn as nothing under the area, never display:none;
//   - a group of a toolbar's buttons (editor-group, <group>, toolbar v2) that follows another starts
//     with a line between them: a hairline in the line colour on its start edge (border: a contrast
//     theme keeps it, in the system's colour), then 0.75rem, the gap the canonical toolbar writes
//     (gap="3") before it, so the line stands halfway; it mirrors on a page written right to left;
//   - in the link's popover, the address (editor-address) takes the room left on its line, at least
//     14em when the popover is that wide, so an address reads whole; the error under it
//     (editor-address-error) is a line of its own;
//   - the tooltip of a toolbar's button (editor-tip, 21/10/2026: one per editor, made and placed by
//     the editor's part jit/js/parts/editor-place.js, the host its offset parent) is small words on one
//     line, inverted like most toolbars' (an ink fill, paper words: the page's own text pair, so it
//     reads as its body text does, in light and in dark), a small radius, above what floats in the
//     editor (z-index 60, a popover's 50); its edge is 1px and transparent, so a contrast theme, which
//     paints the fill as the page's Canvas, draws that edge in the system's colour and the tooltip still
//     ends somewhere. It takes no press (pointer-events: none): where it covers a button (the next row
//     of a toolbar that wraps) or the foot of its own, the button is the pointer's, hovered and pressed
//     as if it were not there. It is still hoverable (WCAG 1.4.13): the part reads the pointer on it, and
//     in the 4px gap to its button, from where the pointer is (21/10/2026; a ::before over the gap took
//     presses meant for the buttons under it, the review's finding).
//   - the plain-text surface of the formats Markdown and HTML (editor-text, 21/10/2026: a <textarea> the
//     editor's part jit/js/parts/editor-format.js makes beside the area, wearing the area's classes but
//     richtext's and its own, shown in its place while the format is not rich) is a block as wide as
//     the area, its words in the code face at the page's size, growing with what it holds where the
//     browser can (field-sizing), at least 4 lines (the part gives it the area's own least height, rows
//     written), resized up and down by hand, at least 16px on a touch screen as the area is;
//   - without JavaScript (scripting: none) the field is the control: shown full width, and the toolbars
//     (editor-toolbar), the area, the popover (editor-popover) and a select of format written beside
//     the toolbar (every control of the editor's: [data-kitwork-command]) are not drawn. !important in the
//     elements layer outranks every class the author's attributes wrote (flex="row" is display:flex in
//     the page layer).

// EditorClass is the class of an editor's host, the key of its scope.
const EditorClass = "editor"

// editorRules is the scope without the heights of rows written, one rule a line.
const editorRules = `.editor{position:relative;min-inline-size:0}
.editor-area{min-block-size:calc(4*1.5em);min-block-size:4lh;cursor:text}
.editor-area[aria-placeholder]:empty::before{content:attr(aria-placeholder);color:color-mix(in oklab,currentColor 70%,var(--paper));pointer-events:none}
.editor-field{position:absolute;inset-block-end:0;inset-inline-start:0;inline-size:1px;block-size:1px;min-inline-size:0;overflow:hidden;clip-path:inset(50%);white-space:nowrap;resize:none}
.editor-group+.editor-group{border-inline-start:1px solid var(--line);padding-inline-start:0.75rem}
.editor-address{flex-grow:1;min-inline-size:min(100%,14em)}
.editor-address-error{inline-size:100%}
.editor-tip{position:absolute;z-index:60;padding:.25rem .5rem;border:1px solid transparent;border-radius:.375rem;background:var(--ink);color:var(--paper);font-size:.75rem;line-height:1.5;white-space:nowrap;pointer-events:none}
.editor-text{display:block;box-sizing:border-box;inline-size:100%;min-block-size:calc(4*1.5em);min-block-size:4lh;margin:0;font:inherit;font-family:var(--font-code,ui-monospace,monospace);line-height:1.5;color:inherit;background:transparent;resize:vertical;field-sizing:content}
@media (pointer:coarse){.editor-area{font-size:max(1em,16px)!important}}
@media (pointer:coarse){.editor-text{font-size:max(1em,16px)!important}}
@media (scripting:none){.editor-area,.editor-toolbar,.editor-popover,.editor [data-kitwork-command]{display:none!important}.editor-field{position:static;inline-size:100%;block-size:auto;overflow:auto;clip-path:none;white-space:pre-wrap;resize:vertical}}
`

// EditorRowsClass is the class of a writing area at least rows lines high (rows="12": editor-rows-12).
func EditorRowsClass(rows int) string { return "editor-rows-" + strconv.Itoa(rows) }

// editorScope is the scope with a rule for each height of rows given, ascending, each once.
func editorScope(rows []int) string {
	var b strings.Builder
	b.WriteString(editorRules)
	seen := map[int]bool{}
	sorted := append([]int(nil), rows...)
	sort.Ints(sorted)
	for _, n := range sorted {
		if seen[n] || n < 1 {
			continue
		}
		seen[n] = true
		lines := strconv.Itoa(n)
		b.WriteString(".editor-area." + EditorRowsClass(n) + "{min-block-size:calc(" + lines + "*1.5em);min-block-size:" + lines + "lh}\n")
	}
	return b.String()
}

// Editor records the look of <editor> in r, with a height for each rows the page's editors write: the
// scope is recorded again whole each time, so the last call holds every height.
func (r *Rules) Editor(rows []int) {
	r.scope(EditorClass, editorScope(rows), nil)
}
