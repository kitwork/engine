package css

type Pattern struct {
	Reg  string
	Type string
}

// THE COMPLETE SOVEREIGN REGISTRY
var Registry = []Pattern{
	// 1. SPECIAL EFFECTS
	{`^(background)-(gradient|grid|haze)-(brand|white|dark)$`, "special-bg"},
	{`^(blur)-(small|medium|large|none)$`, "blur"},
	{`^(shadow)-(small|medium|large|giant|wide|industrial|glow|great|system|core|glow-brand|none)$`, "shadow"},
	{`^(text-clip|glow-brand)$`, "text-effect"},
	{`^(animate)-(pulse|spin|bounce|none)$`, "animate"},

	// 2. SPACING & BOX MODEL
	{`^(margin|padding|gap)-(x|y)-([0-9%.-]+[a-z]*|none|auto)$`, "spacing-axis"},
	{`^(margin|padding)-(top|bottom|left|right)-([0-9%.-]+[a-z]*|none|auto)$`, "spacing-dir"},
	{`^(margin|padding|gap|top|bottom|left|right)-([0-9%.-]+[a-z]*|none|auto)$`, "spacing-single"},
	{`^(width|height|max-width|min-width|max-height|min-height)-([a-z0-9%.-]+|full|screen|auto|fit|min|max)$`, "sizing"},
	{`^(aspect)-(video|square|auto)$`, "aspect"},

	// 3. BORDERS & SHAPES
	{`^(border|outline)-(top|bottom|left|right)-([0-9%.-]+[a-z]*|none)$`, "border-side"},
	{`^(border|outline)-([0-9%.-]+[a-z]*|none)$`, "border-all"},
	{`^(border)-(solid|dashed|dotted|double|none)$`, "border-style"},
	{`^(rounded)-(top|bottom|left|right)-([0-9%.-]+[a-z]*)$`, "rounded-side"},
	{`^(rounded)-([0-9%.-]+[a-z]*|full|none)$`, "rounded-all"},

	// 4. COLORS
	{`^(background|bg|color|text)-([a-zA-Z]+)(?:-([0-9]+))?$`, "color-plain"},
	{`^(border)-(top|bottom|left|right)-([a-zA-Z]+)(?:-([0-9]+))?$`, "border-color-side"},
	{`^(border)-([a-zA-Z]+)(?:-([0-9]+))?$`, "border-color"},

	// 5. TYPOGRAPHY
	{`^(font)-(outfit|mono|bold|medium|light|semibold|black|900|500)$`, "font-family-weight"},
	{`^(text|font-size)-([0-9]+[a-z]*)$`, "font-size"},
	{`^(italic|uppercase|lowercase|capitalize|underline|line-through|no-underline)$`, "text-transform"},
	{`^(text)-(center|left|right|justify)$`, "text-align"},
	{`^(tracking|letter-spacing)-([0-9%.-]+[a-z]*)$`, "letter-spacing"},
	{`^(line-height)-([0-9%.-]+[a-z]*)$`, "line-height"},
	{`^(white-space)-(nowrap|pre|pre-wrap|pre-line|normal)$`, "white-space"},
	{`^(break)-(all|words|normal)$`, "word-break"},

	// 6. LAYOUT (FLEX/GRID)
	{`^(display)-(flex|grid|block|inline-block|none|inline-flex|table|hidden)$`, "display"},
	{`^(flex)-(row|column|wrap|nowrap|grow|1|auto|none)$`, "flex-prop"},
	{`^(justify|items|content)-(start|end|center|between|around|evenly|baseline|stretch)$`, "flex-align"},
	{`^(self)-(auto|start|end|center|stretch)$`, "self-align"},
	{`^(order)-(first|last|none|[0-9]+)$`, "order"},
	{`^(grid-columns)-(\d+|none)$`, "grid-cols"},
	{`^(grid-rows)-(\d+|none)$`, "grid-rows"},
	{`^(grid-span)-(\d+|full)$`, "grid-span"},
	{`^(grid-column)-(start|end)-(\d+|auto)$`, "grid-pos"},

	// 7. INTERACTION & TRANSFORMS
	{`^(position)-(relative|absolute|fixed|sticky|static)$`, "position"},
	{`^(z-index)-([a-z0-9-]+)$`, "z-index"},
	{`^(opacity)-(\d+)$`, "opacity"},
	{`^(cursor)-(pointer|default|not-allowed|text|move|help)$`, "cursor"},
	{`^(pointer-events)-(none|auto)$`, "pointer-events"},
	{`^(select)-(none|text|all|auto)$`, "user-select"},
	{`^(appearance)-(none|auto)$`, "appearance"},
	{`^(resize)-(none|x|y|both)$`, "resize"},

	// 8. TRANSITIONS & ANIMATIONS
	{`^(transition)-(all|none|colors|opacity|transform)$`, "transition"},
	{`^(duration)-([0-9]+)$`, "duration"},
	{`^(delay)-([0-9]+)$`, "delay"},
	{`^(ease)-(linear|in|out|in-out)$`, "ease"},

	// 9. TRANSFORMS
	{`^(translate)-(x|y)-([0-9%.-]+[a-z]*)$`, "translate"},
	{`^(scale)-([0-9.]+)$`, "scale"},
	{`^(scale)-(x|y)-([0-9.]+)$`, "scale-axis"},
	{`^(rotate)-([0-9.-]+)$`, "rotate"},
	{`^(origin)-(center|top|bottom|left|right)$`, "origin"},

	// 10. MISC
	{`^(overflow)-(hidden|auto|scroll|visible|hidden-x|hidden-y|auto-x|auto-y)$`, "overflow"},
	{`^(object)-(contain|cover|fill|none|scale-down)$`, "object-fit"},
	{`^container$`, "container"},
}
