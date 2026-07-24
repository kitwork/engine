package qrcode

import "github.com/fogleman/gg"

type Finders struct {
	TopLeft    Finder `json:"topLeft"`
	TopRight   Finder `json:"topRight"`
	BottomLeft Finder `json:"bottomLeft"`
}

func (f Finders) svg(svg *Svg, mSize int, padding int) {
	f.TopLeft.svg(svg, padding, padding)
	f.TopRight.svg(svg, mSize-7+padding, padding)
	f.BottomLeft.svg(svg, padding, mSize-7+padding)
}

type Finder struct {
	Template string  `json:"template"`
	Color    string  `json:"color"`
	Stroke   string  `json:"stroke"`
	Rounded  float64 `json:"rounded"`
}

func (f Finder) svg(svg *Svg, X, Y int) {
	color := formatColor(f.Color, "#000000")
	stroke := formatColor(f.Stroke, "#000000")

	rounded := f.Rounded

	svg.NewElement("rect").
		XY(float64(X)+0.5, float64(Y)+0.5).
		Width(6).
		Height(6).
		Fill("none").
		Stroke(stroke).
		StrokeWidth(1).
		Rounded(rounded)

	svg.NewElement("rect").
		XY(float64(X)+2.0, float64(Y)+2.0).
		Width(3).
		Height(3).
		Fill(color).
		Rounded(rounded / 2.0)
}

func (f Finder) png(ctx *gg.Context, X, Y int, cellSize float64, template string) {
	xVal := float64(X) * cellSize
	yVal := float64(Y) * cellSize

	color := formatColor(f.Color, "#000000")
	stroke := formatColor(f.Stroke, "#000000")

	tmpl := f.Template
	if tmpl == "" {
		tmpl = template
	}

	fRounded := f.Rounded
	if fRounded <= 0 {
		switch tmpl {
		case "circle", "circular", "dot":
			fRounded = 3.0
		case "rounded":
			fRounded = 1.5
		case "square":
			fRounded = 0.0
		default:
			fRounded = 2.0
		}
	} else if tmpl == "square" {
		fRounded = 0.0
	}
	rOuter := fRounded * cellSize
	rInner := (fRounded / 2.0) * cellSize

	// Outer Frame (stroke width 1.0 * cellSize)
	ctx.SetLineWidth(1.0 * cellSize)
	setGGColor(ctx, stroke, 1.0)
	ctx.DrawRoundedRectangle(xVal+0.5*cellSize, yVal+0.5*cellSize, 6.0*cellSize, 6.0*cellSize, rOuter)
	ctx.Stroke()

	// Inner Dot (fill)
	setGGColor(ctx, color, 1.0)
	ctx.DrawRoundedRectangle(xVal+2.0*cellSize, yVal+2.0*cellSize, 3.0*cellSize, 3.0*cellSize, rInner)
	ctx.Fill()
}

func (f Finders) png(ctx *gg.Context, mSize int, padding int, cellSize float64, template string) {
	f.TopLeft.png(ctx, padding, padding, cellSize, template)
	f.TopRight.png(ctx, mSize-7+padding, padding, cellSize, template)
	f.BottomLeft.png(ctx, padding, mSize-7+padding, cellSize, template)
}
