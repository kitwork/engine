package qrcode

import (
	"github.com/fogleman/gg"
)

type Cell struct {
	Template string  `json:"template"`
	Color    string  `json:"color"`
	Size     float64 `json:"size"`
	Rounded  float64 `json:"rounded"`
	Opacity  float64 `json:"opacity"`
	Hidden   bool    `json:"hidden"`
	Merged   bool    `json:"merged"`
}

type Cells struct {
	Active  Cell
	Passive Cell
}

func (c Cell) svg(svg *Svg, x int, y int) {
	if c.Hidden {
		return
	}

	color := formatColor(c.Color, "#000000")
	if color == "transparent" || color == "none" {
		return
	}

	size := c.Size
	if size <= 0 {
		size = 0
	}

	if size > 1 {
		size = 1
	}
	opacity := c.Opacity
	if opacity <= 0 {
		opacity = 1.0
	}

	cx := float64(x)
	cy := float64(y)

	// Default to rect for square, rounded, empty, or any other templates
	offset := (1.0 - size) / 2.0
	rounded := c.Rounded

	svg.NewElement("rect").
		XY(cx+offset, cy+offset).
		Width(size).
		Height(size).
		Fill(color).
		Opacity(opacity).
		RoundedX(rounded * size)
}

func (c Cells) svg(svg *Svg, grid [][]Module, padding int, showOnLogo bool) {
	mSize := len(grid)
	for y := 0; y < mSize; y++ {
		for x := 0; x < mSize; x++ {
			m := grid[y][x]
			if m.IsFinder() || m.IsAlignment() {
				continue
			}

			if m.IsCenter() && showOnLogo {
				continue
			}

			X := x + padding
			Y := y + padding

			if m.Active() {
				c.Active.svg(svg, X, Y)
			} else {
				if c.Passive.Color != "" {
					c.Passive.svg(svg, X, Y)
				}
			}
		}
	}
}

func (c Cell) png(ctx *gg.Context, x, y int, cellSize float64, template string) {
	if c.Hidden {
		return
	}
	color := formatColor(c.Color, "#000000")
	if color == "transparent" || color == "none" {
		return
	}

	size := c.Size
	if size <= 0 {
		size = 1.0
	}
	opacity := c.Opacity
	if opacity <= 0 {
		opacity = 1.0
	}

	cx := float64(x) * cellSize
	cy := float64(y) * cellSize

	tmpl := c.Template
	if tmpl == "" {
		tmpl = template
	}

	if tmpl == "circle" || tmpl == "circular" || tmpl == "dot" {
		radius := size * cellSize * 0.5
		ctx.DrawCircle(cx+cellSize*0.5, cy+cellSize*0.5, radius)
		setGGColor(ctx, color, opacity)
		ctx.Fill()
		return
	}

	// Default to rect for square, rounded, empty, or any other templates
	offset := (1.0 - size) / 2.0
	X := cx + offset*cellSize
	Y := cy + offset*cellSize
	actualSize := size * cellSize

	roundedness := c.Rounded
	if roundedness <= 0 && tmpl == "rounded" {
		roundedness = 0.25
	} else if tmpl == "square" {
		roundedness = 0
	}

	if roundedness > 0 {
		ctx.DrawRoundedRectangle(X, Y, actualSize, actualSize, roundedness*actualSize)
	} else {
		ctx.DrawRectangle(X, Y, actualSize, actualSize)
	}
	setGGColor(ctx, color, opacity)
	ctx.Fill()
}

func (c Cells) png(ctx *gg.Context, grid [][]Module, padding int, cellSize float64, template string) {
	mSize := len(grid)
	for y := 0; y < mSize; y++ {
		for x := 0; x < mSize; x++ {
			m := grid[y][x]
			if m.IsFinder() || m.IsAlignment() || m.IsCenter() {
				continue
			}

			X := x + padding
			Y := y + padding

			if m.Active() {
				c.Active.png(ctx, X, Y, cellSize, template)
			} else {
				if c.Passive.Color != "" {
					c.Passive.png(ctx, X, Y, cellSize, template)
				}
			}
		}
	}
}
