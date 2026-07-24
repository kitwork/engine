package qrcode

import (
	"github.com/fogleman/gg"
)

type Alignment struct {
	Template string  `json:"template"`
	Color    string  `json:"color"`
	Stroke   string  `json:"stroke"`
	Rounded  float64 `json:"rounded"`
}

func (a Alignment) svg(svg *Svg, grid [][]Module, padding int) {
	mSize := len(grid)
	version := (mSize - 17) / 4
	alignmentCenters := getAlignmentPatternPositions(version)
	if len(alignmentCenters) == 0 {
		return
	}

	rounded := a.Rounded

	stroke := formatColor(a.Stroke, "#000000")
	color := formatColor(a.Color, "#000000")

	for _, cx := range alignmentCenters {
		for _, cy := range alignmentCenters {
			if (cx == 6 && cy == 6) || (cx == 6 && cy == mSize-7) || (cx == mSize-7 && cy == 6) || grid[cy][cx].IsCenter() {
				continue
			}

			X := float64(cx - 2 + padding)
			Y := float64(cy - 2 + padding)

			// Draw Outer Frame
			svg.NewElement("rect").
				XY(X+0.5, Y+0.5).
				Width(4).
				Height(4).
				Fill("none").
				Stroke(stroke).
				StrokeWidth(1).
				Rounded(rounded)

			// Draw Inner Dot
			svg.NewElement("rect").
				XY(X+2.0, Y+2.0).
				Width(1).
				Height(1).
				Fill(color).
				Rounded(rounded / 2.0)
		}
	}
}

func getAlignmentPatternPositions(version int) []int {
	if version <= 1 {
		return []int{}
	}
	numPos := version/7 + 2
	if numPos == 2 {
		return []int{6, version*4 + 10}
	}

	last := version*4 + 10
	positions := make([]int, numPos)
	positions[0] = 6
	positions[numPos-1] = last

	step := (last - 6) / (numPos - 1)
	step = (step + 1) / 2 * 2

	for i := numPos - 2; i > 0; i-- {
		positions[i] = positions[i+1] - step
	}
	return positions
}

func (a Alignment) png(ctx *gg.Context, grid [][]Module, padding int, cellSize float64, template string) {
	mSize := len(grid)
	version := (mSize - 17) / 4
	alignmentCenters := getAlignmentPatternPositions(version)
	if len(alignmentCenters) == 0 {
		return
	}

	tmpl := a.Template
	if tmpl == "" {
		tmpl = template
	}

	alRounded := a.Rounded
	if alRounded <= 0 {
		switch tmpl {
		case "circle", "circular", "dot":
			alRounded = 2.0
		case "rounded":
			alRounded = 1.0
		case "square":
			alRounded = 0.0
		default:
			alRounded = 1.0
		}
	} else if tmpl == "square" {
		alRounded = 0.0
	}

	ctx.SetLineWidth(1.0 * cellSize)

	for _, cx := range alignmentCenters {
		for _, cy := range alignmentCenters {
			if (cx == 6 && cy == 6) || (cx == 6 && cy == mSize-7) || (cx == mSize-7 && cy == 6) || grid[cy][cx].IsCenter() {
				continue
			}

			X := float64(cx-2+padding) * cellSize
			Y := float64(cy-2+padding) * cellSize

			// Outer Frame (stroke width 1.0 * cellSize)
			setGGColor(ctx, formatColor(a.Stroke, "#000000"), 1.0)
			if alRounded > 0 {
				ctx.DrawRoundedRectangle(X+0.5*cellSize, Y+0.5*cellSize, 4.0*cellSize, 4.0*cellSize, alRounded*cellSize)
			} else {
				ctx.DrawRectangle(X+0.5*cellSize, Y+0.5*cellSize, 4.0*cellSize, 4.0*cellSize)
			}
			ctx.Stroke()

			// Inner Dot (fill)
			setGGColor(ctx, formatColor(a.Color, "#000000"), 1.0)
			if alRounded > 0 {
				ctx.DrawRoundedRectangle(X+2.0*cellSize, Y+2.0*cellSize, 1.0*cellSize, 1.0*cellSize, (alRounded/2.0)*cellSize)
			} else {
				ctx.DrawRectangle(X+2.0*cellSize, Y+2.0*cellSize, 1.0*cellSize, 1.0*cellSize)
			}
			ctx.Fill()
		}
	}
}
