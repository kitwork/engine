package css

type Pattern struct {
	Reg  string
	Type string
}

// THE COMPLETE SOVEREIGN REGISTRY
//
// Tailwind patterns are listed FIRST so their short class names (bg-gray-900, text-5xl,
// gap-3, flex …) win over the custom "industrial" system, which uses long, non-colliding
// names (background-brand, text-16px, gap-16px, display-flex). The custom patterns only
// match their own verbose names, so a tw-first order never steals from them.
var Registry = []Pattern{
	// ===================== TAILWIND ALIASES & ARBITRARY VALUES =====================
	// Spacing: directional (mt/mb/ml/mr/pt/pb/pl/pr), axis (mx/my/px/py), all (m/p),
	// gap, and space-between — each with negatives, decimals (0.5), fractions, arbitrary.
	{`^-?(mt|mr|mb|ml|pt|pr|pb|pl)-([\d.]+|auto|full|px|\[.+?\])$`, "tw-side"},
	{`^-?(mx|my|px|py)-([\d.]+|auto|full|px|\[.+?\])$`, "tw-axis"},
	{`^-?(m|p)-([\d.]+|auto|px|\[.+?\])$`, "tw-allside"},
	{`^(gap)-(x|y)-([\d.]+|px|\[.+?\])$`, "tw-gap-axis"},
	{`^(gap)-([\d.]+|px|\[.+?\])$`, "tw-gap"},
	{`^(leading)-(none|tight|snug|normal|relaxed|loose|[\d.]+|\[.+?\])$`, "tw-leading"},
	{`^(tracking)-(tighter|tight|normal|wide|wider|widest|\[.+?\])$`, "tw-tracking"},
	{`^(font)-([a-z][a-z0-9-]*)$`, "tw-font-family"},
	{`^\[(.+:.+)\]$`, "tw-arbitrary-prop"},
	{`^(?:flex-)?(shrink|grow)(?:-(0))?$`, "tw-shrink-grow"},
	{`^(transform|transform-gpu|transform-none|group|peer|sr-only|antialiased|isolate|box-border|box-content)$`, "tw-marker"},
	{`^(backdrop-blur)(?:-(sm|md|lg|xl|2xl|3xl|none|\[.+?\]))?$`, "tw-backdrop-blur"},
	{`^-?(translate)-(x|y)-([\d.]+|full|px|\d+/\d+|\[.+?\])$`, "tw-translate"},
	{`^(w|h|max-w|min-w|max-h|min-h)-([\d.]+|full|screen|auto|fit|min|max|\d+/\d+|\[.+?\]|[a-z0-9-]+)$`, "tw-sizing"},
	// ring-* must come before the colour patterns: `ring-offset-canvas` would otherwise be read as the
	// colour "offset-canvas". Width/inset/offset compose a box-shadow from CSS variables; the colour
	// forms below only set --kitwork-ring-color, never box-shadow directly.
	{`^ring$`, "tw-ring-width"},
	{`^ring-(\d+)$`, "tw-ring-width"},
	{`^ring-inset$`, "tw-ring-inset"},
	{`^ring-offset-(\d+)$`, "tw-ring-offset-width"},
	{`^ring-offset-([a-z]+)-(\d+)(?:/(\d+|\[.+?\]))?$`, "tw-ring-offset-shade"},
	{`^ring-offset-([a-z][a-z-]*)(?:/(\d+|\[.+?\]))?$`, "tw-ring-offset-base"},
	{`^border-(t|b|l|r|x|y)-([a-z]+)-(\d+)(?:/(\d+|\[.+?\]))?$`, "tw-border-side-shade"},
	{`^border-(t|b|l|r|x|y)-([a-z][a-z-]*)(?:/(\d+|\[.+?\]))?$`, "tw-border-side-base"},
	{`^stroke-(\d+(?:\.\d+)?)$`, "tw-stroke-width"},
	{`^stroke-\[(\d+(?:\.\d+)?)\]$`, "tw-stroke-width"},
	{`^border-(solid|dashed|dotted|double|hidden|none)$`, "tw-border-style"},
	// ===================== KEYWORD UTILITIES (must precede the colour patterns) =====================
	// `bg-cover`, `text-ellipsis`, `border-collapse` and friends all look like "<prefix>-<colour>" to
	// the two patterns below. twColor returns "" for them so the loop would move on, but listing them
	// first keeps the intent visible and skips the wasted lookup.
	{"^(visible|invisible|collapse)$", "tw-visibility"},
	{"^align-(baseline|top|middle|bottom|text-top|text-bottom|sub|super)$", "tw-vertical-align"},
	{"^appearance-(none|auto)$", "tw-appearance"},
	{"^bg-(cover|contain)$", "tw-bg-size"},
	{"^bg-(center|top|bottom|left|right|left-top|left-bottom|right-top|right-bottom)$", "tw-bg-position"},
	{"^bg-(repeat|no-repeat|repeat-x|repeat-y|repeat-round|repeat-space)$", "tw-bg-repeat"},
	{"^bg-(fixed|local|scroll)$", "tw-bg-attachment"},
	{"^bg-origin-(border|padding|content)$", "tw-bg-origin"},
	{"^bg-none$", "tw-bg-none"},
	{"^object-(center|top|bottom|left|right|left-top|left-bottom|right-top|right-bottom)$", "tw-object-position"},
	{"^resize(?:-(none|x|y))?$", "tw-resize"},
	{"^scroll-(smooth|auto)$", "tw-scroll-behavior"},
	{"^snap-(none|x|y|both)$", "tw-snap-type"},
	{"^snap-(mandatory|proximity)$", "tw-snap-strictness"},
	{"^snap-(start|end|center|align-none)$", "tw-snap-align"},
	{"^snap-(normal|always)$", "tw-snap-stop"},
	{"^(normal-nums|ordinal|slashed-zero|lining-nums|oldstyle-nums|proportional-nums|tabular-nums|diagonal-fractions|stacked-fractions)$", "tw-numeric"},
	{"^float-(left|right|none|start|end)$", "tw-float"},
	{"^clear-(left|right|both|none|start|end)$", "tw-clear"},
	{"^table-(auto|fixed)$", "tw-table-layout"},
	{"^border-(collapse|separate)$", "tw-border-collapse"},
	{`^border-spacing-(x|y)-([\d.]+|px|\[.+?\])$`, "tw-border-spacing-axis"},
	{`^border-spacing-([\d.]+|px|\[.+?\])$`, "tw-border-spacing"},
	{"^caption-(top|bottom)$", "tw-caption"},
	{"^text-(ellipsis|clip)$", "tw-text-overflow"},
	{"^text-(wrap|nowrap|balance|pretty)$", "tw-text-wrap"},
	{"^list-(inside|outside)$", "tw-list-position"},
	{"^list-image-none$", "tw-list-image"},
	{`^decoration-(\d+|auto|from-font)$`, "tw-decoration-thickness"},
	{"^decoration-(solid|double|dotted|dashed|wavy)$", "tw-decoration-style"},
	{"^outline-(solid|dashed|dotted|double)$", "tw-outline-style"},
	{"^justify-self-(auto|start|end|center|stretch)$", "tw-justify-self"},
	{"^justify-items-(start|end|center|stretch)$", "tw-justify-items"},
	{`^basis-([\d.]+|auto|full|px|\d+/\d+|\[.+?\])$`, "tw-basis"},
	{"^(auto-cols|auto-rows)-(auto|min|max|fr)$", "tw-auto-track"},
	{"^grid-flow-(row-dense|col-dense|row|col|dense)$", "tw-grid-flow"},
	{"^(overscroll|overscroll-x|overscroll-y)-(auto|contain|none)$", "tw-overscroll"},
	{"^touch-(auto|none|pan-x|pan-left|pan-right|pan-y|pan-up|pan-down|pinch-zoom|manipulation)$", "tw-touch"},
	{"^will-change-(auto|scroll|contents|transform)$", "tw-will-change"},
	{`^-?indent-([\d.]+|px|\[.+?\])$`, "tw-indent"},
	{`^columns-(\d+|auto|\[.+?\])$`, "tw-columns"},
	{"^mix-blend-([a-z-]+)$", "tw-mix-blend"},
	{"^bg-blend-([a-z-]+)$", "tw-bg-blend"},
	{"^(break-before|break-after)-(auto|avoid|all|avoid-page|page|left|right|column)$", "tw-break-side"},
	{"^break-inside-(auto|avoid|avoid-page|avoid-column)$", "tw-break-inside"},
	{"^box-decoration-(clone|slice)$", "tw-box-decoration"},
	{"^hyphens-(none|manual|auto)$", "tw-hyphens"},
	{"^(not-sr-only|subpixel-antialiased|isolation-auto)$", "tw-misc"},
	{"^content-none$", "tw-content-none"},
	{`^content-\[(.+?)\]$`, "tw-content-arb"},
	{"^(divide|space)-(x|y)-reverse$", "tw-reverse"},
	{`^caret-([a-z]+)-(\d+)(?:/(\d+|\[.+?\]))?$`, "tw-caret-shade"},
	{"^caret-([a-z][a-z-]*)$", "tw-caret-base"},
	{`^placeholder-([a-z]+)-(\d+)(?:/(\d+|\[.+?\]))?$`, "tw-placeholder-shade"},
	{`^placeholder-([a-z][a-z-]*)(?:/(\d+|\[.+?\]))?$`, "tw-placeholder-base"},
	// Filters compose through --kitwork-* variables the way Tailwind composes through its own, so
	// `blur-sm grayscale` keeps both instead of the second `filter:` declaration silently replacing
	// the first.
	{"^filter$", "tw-filter"},
	{"^filter-none$", "tw-filter-none"},
	{"^(grayscale|invert|sepia)(?:-(0))?$", "tw-filter-toggle"},
	{`^(brightness|contrast|saturate)-(\d+|\[.+?\])$`, "tw-filter-pct"},
	{`^-?(hue-rotate)-(\d+|\[.+?\])$`, "tw-filter-hue"},
	{`^drop-shadow(?:-(sm|md|lg|xl|2xl|none|\[.+?\]))?$`, "tw-drop-shadow"},
	{"^backdrop-(grayscale|invert|sepia)(?:-(0))?$", "tw-backdrop-toggle"},
	{`^backdrop-(brightness|contrast|saturate|opacity)-(\d+|\[.+?\])$`, "tw-backdrop-pct"},
	{`^-?backdrop-(hue-rotate)-(\d+|\[.+?\])$`, "tw-backdrop-hue"},
	{"^backdrop-filter$", "tw-backdrop-filter"},
	{"^backdrop-filter-none$", "tw-backdrop-filter-none"},
	{`^(bg|text|border|ring|outline|decoration|accent|fill|stroke)-([a-z]+)-(\d+)(?:/(\d+|\[.+?\]))?$`, "tw-color-shade"},
	{`^(bg|text|border|ring|outline|decoration|accent|fill|stroke)-([a-z][a-z-]*)(?:/(\d+|\[.+?\]))?$`, "tw-color-base"},
	{`^(bg|text|border|decoration|accent|ring|outline|divide|fill|stroke|caret|placeholder|shadow)-\[(#[0-9a-fA-F]+)\](?:/(\d+|\[[\d.]+\]))?$`, "tw-color-arbitrary"},
	// bg-[…] that is NOT a colour. Tailwind resolves this class by inspecting the
	// value — url() and the gradient functions are images, a colour is a colour —
	// and only the colour half existed, so a gradient produced no rule at all.
	// Listed after the hex rule so colours are still claimed there first; the
	// handler answers only shapes it can type with certainty and returns nothing
	// for the rest, which is what they already got.
	{`^bg-\[(.+)\]$`, "tw-bg-arbitrary"},
	// gradients
	{`^bg-gradient-to-(t|b|l|r|tl|tr|bl|br)$`, "tw-gradient-dir"},
	{`^(from|via|to)-([a-z]+)-(\d+)(?:/(\d+|\[.+?\]))?$`, "tw-gradient-stop"},
	{`^(from|via|to)-([a-z][a-z-]*)(?:/(\d+|\[.+?\]))?$`, "tw-gradient-stop-base"},
	{`^(from|via|to)-\[(#[0-9a-fA-F]+)\]$`, "tw-gradient-stop-arb"},
	{`^bg-clip-(text|border|padding|content)$`, "tw-bg-clip"},
	// space-between + divide (child selectors, applied in ResolveCore)
	{`^space-(x|y)-(-?[\d.]+|px|\[.+?\])$`, "tw-space"},
	{`^divide-(x|y)$`, "tw-divide"},
	{`^divide-(x|y)-(\d+)$`, "tw-divide-width"},
	{`^divide-(solid|dashed|dotted|double|none)$`, "tw-divide-style"},
	{`^divide-\[(#[0-9a-fA-F]+)\](?:/(\d+|\[[\d.]+\]))?$`, "tw-divide-arb"},
	{`^divide-([a-z]+)-(\d+)(?:/(\d+|\[.+?\]))?$`, "tw-divide-shade"}, // divide-gray-200[/40]
	{`^divide-([a-z][a-z-]*)(?:/(\d+|\[.+?\]))?$`, "tw-divide-base"},  // divide-line[/40], divide-brand-soft
	// outline
	{`^outline$`, "tw-outline"},
	{`^outline-none$`, "tw-outline-none"},
	{`^outline-(\d+)$`, "tw-outline-width"},
	{`^-?outline-offset-(\d+|\[.+?\])$`, "tw-outline-offset"},
	// scroll margin/padding
	{`^scroll-(mt|mr|mb|ml|mx|my|m|pt|pr|pb|pl|px|py|p)-([\d.]+|px|\[.+?\])$`, "tw-scroll"},
	{`^(rounded)-?(t|b|l|r|tl|tr|bl|br)?-?(sm|md|lg|xl|2xl|3xl|full|none|\[.+?\])?$`, "tw-rounded"},
	{`^(text)-(\d*xs|sm|base|md|lg|\d*xl|\[.+?\])$`, "tw-text-size"},
	{`^(blur)-?(sm|md|lg|xl|2xl|3xl|none|\[.+?\])?$`, "tw-blur"},
	{`^(opacity)-(\d+|\[.+?\])$`, "tw-opacity"},
	{`^(grid-cols)-(\d+|none|\[.+?\])$`, "tw-grid-cols"},
	{`^(col-span)-(\d+|full)$`, "tw-col-span"},
	{`^(flex)-(row-reverse|col-reverse|wrap-reverse|row|col|wrap|nowrap|1|auto|initial|none)$`, "tw-flex"},
	{`^(justify|items|content|self)-(start|end|center|between|around|evenly|stretch|baseline|auto)$`, "tw-align"},
	{`^place-(items|content|self)-(start|end|center|between|around|evenly|stretch|baseline|auto)$`, "tw-place"},
	{`^(block|inline-block|inline|flex|inline-flex|grid|inline-grid|hidden|contents|flow-root|list-item|inline-table|table-row-group|table-header-group|table-footer-group|table-row|table-cell|table-column-group|table-column|table-caption|table)$`, "tw-display"},
	{`^(static|fixed|absolute|relative|sticky)$`, "tw-position"},
	{`^(top|right|bottom|left|inset|inset-x|inset-y)-([\d.]+|auto|full|px|\d+/\d+|\[.+?\])$`, "tw-inset"},
	{`^-?(top|right|bottom|left|inset|inset-x|inset-y)-([\d.]+|px|\d+/\d+|\[.+?\])$`, "tw-inset-neg"},
	{`^(z)-(\d+|auto|\[.+?\])$`, "tw-zindex"},
	{`^-?(z)-(\d+|\[.+?\])$`, "tw-zindex-neg"},
	{`^(shadow)(?:-([a-z0-9][a-z0-9-]*))?$`, "tw-shadow"},
	{`^shadow-([a-z]+)-(\d+)(?:/(\d+|\[.+?\]))?$`, "tw-shadow-color-shade"},
	{`^shadow-([a-z][a-z-]*)(?:/(\d+|\[.+?\]))?$`, "tw-shadow-color-base"},
	{`^(overflow|overflow-x|overflow-y)-(auto|hidden|clip|visible|scroll)$`, "tw-overflow"},
	{`^(cursor)-(auto|default|pointer|wait|text|move|help|not-allowed)$`, "tw-cursor"},
	{`^(transition)(?:-(all|colors|opacity|shadow|transform|none))?$`, "tw-transition"},
	{`^(duration)-(\d+|\[.+?\])$`, "tw-duration"},
	{`^(delay)-(\d+|\[.+?\])$`, "tw-delay"},
	{`^(ease)-(linear|in|out|in-out|\[.+?\])$`, "tw-ease"},
	{`^(border)(?:-(t|b|l|r|x|y))?(?:-(\d+|\[.+?\]))?$`, "tw-border"},
	{`^(font)-(thin|extralight|light|normal|medium|semibold|bold|extrabold|black)$`, "tw-font-weight"},
	{`^(text)-(left|center|right|justify|start|end)$`, "tw-text-align"},
	{`^list-(none|disc|decimal)$`, "tw-list-style"},
	{`^(italic|not-italic|uppercase|lowercase|capitalize|normal-case|underline|line-through|no-underline)$`, "tw-text-decor"},
	{`^underline-offset-(auto|\d+)$`, "tw-underline-offset"},
	// transforms
	{`^-?(rotate)-(\d+|\[.+?\])$`, "tw-rotate"},
	{`^-?(scale)-(\d+|\[.+?\])$`, "tw-scale"},
	{`^-?(scale)-(x|y)-(\d+|\[.+?\])$`, "tw-scale-axis"},
	{`^-?(skew)-(x|y)-(\d+|\[.+?\])$`, "tw-skew"},
	{`^origin-(center|top|bottom|left|right|top-left|top-right|bottom-left|bottom-right)$`, "tw-origin"},
	// aspect / object-fit
	{`^aspect-(video|square|auto)$`, "tw-aspect"},
	{`^aspect-(\d+)/(\d+)$`, "tw-aspect-fraction"},
	{`^aspect-\[(.+?)\]$`, "tw-aspect-arb"},
	{`^object-(contain|cover|fill|none|scale-down)$`, "tw-object"},
	// text overflow / whitespace / clamp / break
	{`^truncate$`, "tw-truncate"},
	{`^whitespace-(normal|nowrap|pre|pre-line|pre-wrap|break-spaces)$`, "tw-whitespace"},
	{`^line-clamp-(\d+|none)$`, "tw-line-clamp"},
	{`^break-(words|all|normal|keep)$`, "tw-break"},
	// grid placement
	{`^(col|row)-(start|end)-(\d+|auto)$`, "tw-grid-line"},
	{`^row-span-(\d+|full)$`, "tw-row-span"},
	{`^(grid-rows)-(\d+|none|\[.+?\])$`, "tw-grid-rows"},
	// shadow arbitrary
	{`^shadow-\[(.+?)\]$`, "tw-shadow-arb"},
	// flex order / interaction / animation
	{`^-?order-(\d+|first|last|none)$`, "tw-order"},
	{`^pointer-events-(none|auto)$`, "tw-pointer-events"},
	{`^select-(none|text|all|auto)$`, "tw-select"},
	{`^animate-\[(.+?)\]$`, "tw-animate-arb"},
	{`^animate-on-hover$`, "tw-animate-onhover"},
	{`^animate-([a-zA-Z0-9_-]+)$`, "tw-animate"},
	{`^container$`, "container"},
}
