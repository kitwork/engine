package stylesheet

import (
	"strings"

	"github.com/kitwork/engine/utilities/richtext"
)

// The text scope of <richtext> (decisions of 20/10/2026): a post the site saved, cleaned on the server
// into a closed grammar (utilities/richtext: p, h2–h6, blockquote, ul, ol, li, pre holding one code,
// hr, strong, em, u, s, code, a, br; class align-center or align-right on a p or a heading), is
// written inside <div class="richtext">. Nothing inside carries a class but an alignment, so how it
// reads is this scope: the blocks apart by a line of their own size, more room above a heading than
// below it and around a rule, list markers back (reboot.css takes them off every list), links
// underlined in the brand made readable, a quote set off by a line on its left (not the quiet fill a
// <quote> has), h2–h6 on the heading scale of the text around them, strong text bold, underlined text
// underlined in its own colour (a link is the brand's), struck text struck through, code in a line on a
// light fill of ink with small corners, a code block on the panel with room inside, scrolling sideways
// and keeping its white space, a rule a hairline in the line colour, and the alignments: align-center
// centred, align-right text-align end (so it mirrors on a page written right to left, as align="right"
// on <text> does). Code inside a link has no fill: it is the link's words in the code face. The link's
// colour is made to read 4.5:1 on canvas, paper and panel, not on the fill, and in dark the fill takes
// it under that on many palettes (measured 20/10/2026 on paper: brand #0070f3, #2563eb, #7c3aed and
// #db2777 at 3.9 to 4.0:1).
//
// The rules are the page's, in page.<hash>.css, only on a page that holds a richtext (Table.Richtext),
// inside the elements layer: each weighs one class, the richtext's, so inside it the scope beats the
// element looks of reboot.css (:where(blockquote), :where(h2), :where(pre)) whatever order the files
// arrive in, and every class an attribute writes, in the page layer, beats the scope. Sizes and room
// are in em: a richtext written size="large" grows its headings and its room with its words. The two
// alignment classes are the same words align="center" and align="right" write on a <text> (the page
// layer's text-align:center and text-align:end): where a page has both, they agree.
//
// The words inside a richtext are the colour around it, measured where the richtext stands like any
// text (ui/contrast.go); a link's words are the engine colour kitwork-link (engine_colour.go), measured
// the same way as RichtextLinkLook, and the words of code on their fills as RichtextCodeLook and
// RichtextCodeBlockLook. The quote's line, the rule and the list markers say nothing of their own.

// RichtextClass is the class of the element <richtext> becomes, the scope of its rules.
const RichtextClass = "richtext"

// RichtextCodeFill is the fill behind code in a line of a richtext, ink at 8% (a light chip of the text's
// own ink, over whatever ground the richtext stands on); RichtextCodeBlockFill is a code block's, the
// palette's panel. Both are colour values of the table: the scope's CSS and the contrast check read the
// same ones.
const (
	RichtextCodeFill      = "ink/8"
	RichtextCodeBlockFill = "panel"
)

// richtextRules is the scope, one rule a line; the link's colour is LINK, the fill of code CODE and of a
// code block BLOCK until Table.Richtext writes the colours in their place. The link's rule stays last.
const richtextRules = `.richtext{min-inline-size:0}
.richtext>*+*{margin-block-start:1em}
.richtext>*+:where(h2,h3,h4,h5,h6){margin-block-start:1.5em}
.richtext>:where(h2,h3,h4,h5,h6)+*{margin-block-start:0.5em}
.richtext>:where(*+hr,hr+*){margin-block-start:1.5em}
.richtext :where(h2){font-size:1.5em}
.richtext :where(h3){font-size:1.125em}
.richtext :where(h4){font-size:1em}
.richtext :where(h5){font-size:0.875em}
.richtext :where(h6){font-size:0.8125em}
.richtext :where(ul,ol){padding-inline-start:1.5em}
.richtext :where(ul){list-style:disc}
.richtext :where(ol){list-style:decimal}
.richtext :where(li+li){margin-block-start:0.25em}
.richtext :where(blockquote){padding:0;padding-inline-start:1em;border:0;border-inline-start:0.25em solid var(--line);border-radius:0;background:none}
.richtext :where(blockquote>*+*){margin-block-start:1em}
.richtext :where(strong){font-weight:700}
.richtext :where(u){text-decoration:underline;text-underline-offset:0.15em}
.richtext :where(s){text-decoration:line-through}
.richtext :where(code:not(pre code,a code)){padding:0.125em 0.25em;border-radius:0.25em;background:CODE}
.richtext :where(pre){padding:1em;border-radius:0.5em;background:BLOCK;overflow-x:auto;white-space:pre}
.richtext :where(hr){border:0;border-block-start:1px solid var(--line)}
.richtext :where(.` + richtext.AlignCenter + `){text-align:center}
.richtext :where(.` + richtext.AlignRight + `){text-align:end}
.richtext :where(a){color:LINK;text-decoration:underline;text-underline-offset:0.15em}
`

// richtextWeights are the weights the scope's text takes besides running text's 400: a heading's 600
// (reboot.css) and strong text's 700, which fonts.<hash>.css ships for a page that holds a richtext.
var richtextWeights = []int{600, 700}

// RichtextLinkLook is how a link inside a richtext looks, as the contrast check reads it: its words in
// the engine colour kitwork-link, at the size and weight of the text around it.
func RichtextLinkLook() Look { return Look{Text: LinkColour} }

// RichtextCodeLook is how code in a line of a richtext looks, as the contrast check reads it: the words
// around it (a link's when it stands in one) on ink/8 over the ground around.
func RichtextCodeLook() Look { return Look{Surface: RichtextCodeFill} }

// RichtextCodeBlockLook is how a code block of a richtext looks, as the contrast check reads it: the
// words around it on the panel.
func RichtextCodeBlockLook() Look { return Look{Surface: RichtextCodeBlockFill} }

// Richtext records the text scope of <richtext> in r (nil only checks it can be written): its rules,
// the custom property of kitwork-link per mode, and the weights 600 and 700. An error is a palette that
// cannot name the link's colour or the fills of code (every palette can: brand, ink and panel are Kit
// Design's, which router.colors only overrides).
func (t *Table) Richtext(r *Rules) error {
	link, pairs, ok := t.engineColour(LinkColour)
	if !ok {
		return &Problem{Message: "bảng màu không có brand để tô link trong <richtext>"}
	}
	code, _, _, _, p := t.colour(Intent{Name: "background"}, RichtextCodeFill)
	if p != nil {
		return p
	}
	block, _, _, _, p := t.colour(Intent{Name: "background"}, RichtextCodeBlockFill)
	if p != nil {
		return p
	}
	if r != nil {
		css := strings.NewReplacer("LINK", link, "CODE", code, "BLOCK", block).Replace(richtextRules)
		r.scope(RichtextClass, css, pairs, richtextWeights...)
	}
	return nil
}
