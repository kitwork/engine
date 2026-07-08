package qrcode

import (
	"math"

	"github.com/fogleman/gg"
)

type Background struct {
	Size    int     `json:"size"`
	Color   string  `json:"color"`
	Rounded float64 `json:"rounded"`
	Stroke  string  `json:"stroke,omitempty"`
	Border  float64 `json:"border,omitempty"`
	Dashed  string  `json:"dashed,omitempty"`
}

func (b Background) svg(svg *Svg) {
	size := float64(b.Size)
	if size <= 0 {
		return
	}
	color := formatColor(b.Color, "#ffffff")
	rounded := b.Rounded

	stroke := formatColor(b.Stroke)
	border := b.Border

	svg.NewElement("rect").
		Data("name", "background").
		Width(size).
		Height(size).
		Rounded(rounded).
		Fill(color).
		RoundedX(rounded).
		Stroke(stroke).
		DashArray(b.DashArray(size)).
		StrokeWidth(border).
		DashOffset(b.DashOffset(float64(size)))
}

func (b Background) perimeter(size float64) float64 {
	return 4*(size-b.Border) - 8*b.Border + 2*math.Pi*b.Border
}

func (b Background) DashArray(size float64) float64 {
	switch b.Dashed {
	case "cornered", "circular":
		return b.perimeter(size) / 8
	}
	return 0
}

func (b Background) DashOffset(size float64) float64 {
	switch b.Dashed {
	case "cornered":
		return (b.DashArray(size) / 2) + (b.Border * math.Pi / 4)
	case "circular":
		return b.DashArray(size) / 2
	}
	return 0
}

func (b Background) png(ctx *gg.Context, cellSize float64) {
	size := float64(b.Size)
	if size <= 0 {
		return
	}
	imgSize := size * cellSize
	color := formatColor(b.Color, "#ffffff")
	rounded := b.Rounded * cellSize

	if color != "transparent" && color != "none" {
		if rounded > 0 {
			ctx.DrawRoundedRectangle(0, 0, imgSize, imgSize, rounded)
		} else {
			ctx.DrawRectangle(0, 0, imgSize, imgSize)
		}
		setGGColor(ctx, color, 1.0)
		ctx.Fill()
	}

	stroke := formatColor(b.Stroke)
	border := b.Border * cellSize
	if stroke != "" && stroke != "transparent" && stroke != "none" && border > 0 {
		rx := rounded
		if b.Dashed == "circular" {
			rx = (imgSize - border) / 2.0
		}

		ctx.SetLineWidth(border)
		setGGColor(ctx, stroke, 1.0)

		isDashed := b.Dashed == "cornered" || b.Dashed == "circular"
		if isDashed {
			dashVal := b.DashArray(size) * cellSize
			offsetVal := b.DashOffset(size) * cellSize
			ctx.SetDash(dashVal, dashVal)
			ctx.SetDashOffset(offsetVal)
		}

		ctx.DrawRoundedRectangle(border/2.0, border/2.0, imgSize-border, imgSize-border, rx)
		ctx.Stroke()

		if isDashed {
			ctx.SetDash() // Reset to solid
		}
	}
}
