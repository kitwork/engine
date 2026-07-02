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
	{`^(font)-(sans|serif|mono)$`, "tw-font-family"},
	{`^(shrink|grow)(?:-(0))?$`, "tw-shrink-grow"},
	{`^(transform|transform-gpu|transform-none|group|peer|sr-only|antialiased|isolate)$`, "tw-marker"},
	{`^(backdrop-blur)(?:-(sm|md|lg|xl|2xl|3xl|none|\[.+?\]))?$`, "tw-backdrop-blur"},
	{`^-?(translate)-(x|y)-([\d.]+|full|px|\d+/\d+|\[.+?\])$`, "tw-translate"},
	{`^(w|h|max-w|min-w|max-h|min-h)-([\d.]+|full|screen|auto|fit|min|max|\d+/\d+|\[.+?\]|[a-z0-9-]+)$`, "tw-sizing"},
	{`^(bg|text|border|ring|outline)-([a-z]+)-(\d+)(?:/(\d+|\[.+?\]))?$`, "tw-color-shade"},
	{`^(bg|text|border|ring|outline)-([a-z][a-z-]*)(?:/(\d+|\[.+?\]))?$`, "tw-color-base"},
	{`^(bg|text|border)-\[(#[0-9a-fA-F]+)\](?:/(\d+))?$`, "tw-color-arbitrary"},
	// gradients
	{`^bg-gradient-to-(t|b|l|r|tl|tr|bl|br)$`, "tw-gradient-dir"},
	{`^(from|via|to)-([a-z]+)-(\d+)$`, "tw-gradient-stop"},
	{`^(from|via|to)-([a-z][a-z-]*)$`, "tw-gradient-stop-base"},
	{`^(from|via|to)-\[(#[0-9a-fA-F]+)\]$`, "tw-gradient-stop-arb"},
	{`^bg-clip-(text|border|padding|content)$`, "tw-bg-clip"},
	// space-between + divide (child selectors, applied in ResolveCore)
	{`^space-(x|y)-(-?[\d.]+|px|\[.+?\])$`, "tw-space"},
	{`^divide-(x|y)$`, "tw-divide"},
	{`^divide-([a-z]+)-(\d+)$`, "tw-divide-color"},
	// outline
	{`^outline$`, "tw-outline"},
	{`^outline-(\d+)$`, "tw-outline-width"},
	{`^outline-offset-(\d+)$`, "tw-outline-offset"},
	// scroll margin/padding
	{`^scroll-(mt|mr|mb|ml|mx|my|m|pt|pr|pb|pl|px|py|p)-([\d.]+|px|\[.+?\])$`, "tw-scroll"},
	{`^(rounded)-?(t|b|l|r|tl|tr|bl|br)?-?(sm|md|lg|xl|2xl|3xl|full|none|\[.+?\])?$`, "tw-rounded"},
	{`^(text)-(\d*xs|sm|base|md|lg|\d*xl|\[.+?\])$`, "tw-text-size"},
	{`^(blur)-?(sm|md|lg|xl|2xl|3xl|none|\[.+?\])?$`, "tw-blur"},
	{`^(opacity)-(\d+|\[.+?\])$`, "tw-opacity"},
	{`^(grid-cols)-(\d+|none|\[.+?\])$`, "tw-grid-cols"},
	{`^(col-span)-(\d+|full)$`, "tw-col-span"},
	{`^(flex)-(row|col|wrap|nowrap|1|auto|initial|none)$`, "tw-flex"},
	{`^(justify|items|content|self)-(start|end|center|between|around|evenly|stretch|baseline)$`, "tw-align"},
	{`^(block|inline-block|inline|flex|inline-flex|grid|inline-grid|hidden|table)$`, "tw-display"},
	{`^(static|fixed|absolute|relative|sticky)$`, "tw-position"},
	{`^(top|right|bottom|left|inset|inset-x|inset-y)-([\d.]+|auto|full|px|\d+/\d+|\[.+?\])$`, "tw-inset"},
	{`^-?(top|right|bottom|left|inset|inset-x|inset-y)-([\d.]+|px|\d+/\d+|\[.+?\])$`, "tw-inset-neg"},
	{`^(z)-(\d+|auto|\[.+?\])$`, "tw-zindex"},
	{`^-?(z)-(\d+|\[.+?\])$`, "tw-zindex-neg"},
	{`^(shadow)(?:-(sm|md|lg|xl|2xl|inner|none))?$`, "tw-shadow"},
	{`^(overflow|overflow-x|overflow-y)-(auto|hidden|clip|visible|scroll)$`, "tw-overflow"},
	{`^(cursor)-(auto|default|pointer|wait|text|move|help|not-allowed)$`, "tw-cursor"},
	{`^(transition)(?:-(all|colors|opacity|shadow|transform|none))?$`, "tw-transition"},
	{`^(duration)-(\d+|\[.+?\])$`, "tw-duration"},
	{`^(ease)-(linear|in|out|in-out|\[.+?\])$`, "tw-ease"},
	{`^(border)(?:-(t|b|l|r|x|y))?(?:-(\d+|\[.+?\]))?$`, "tw-border"},
	{`^(font)-(thin|extralight|light|normal|medium|semibold|bold|extrabold|black)$`, "tw-font-weight"},
	{`^(text)-(left|center|right|justify|start|end)$`, "tw-text-align"},
	{`^(italic|not-italic|uppercase|lowercase|capitalize|normal-case|underline|line-through|no-underline)$`, "tw-text-decor"},
	// transforms
	{`^-?(rotate)-(\d+|\[.+?\])$`, "tw-rotate"},
	{`^-?(scale)-(\d+|\[.+?\])$`, "tw-scale"},
	{`^-?(scale)-(x|y)-(\d+|\[.+?\])$`, "tw-scale-axis"},
	{`^-?(skew)-(x|y)-(\d+|\[.+?\])$`, "tw-skew"},
	{`^origin-(center|top|bottom|left|right|top-left|top-right|bottom-left|bottom-right)$`, "tw-origin"},
	// aspect / object-fit
	{`^aspect-(video|square|auto)$`, "tw-aspect"},
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
	{`^animate-([a-zA-Z0-9_-]+)$`, "tw-animate"},
	{`^container$`, "container"},
}
