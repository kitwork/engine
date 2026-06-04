package qrcode

import (
	"strings"

	goqrcode "github.com/skip2/go-qrcode"
)

func formatColor(c string, defs ...string) string {
	def := "none"
	if len(defs) > 0 {
		def = defs[0]
	}
	if c == "" {
		return def
	}
	if c == "transparent" || c == "none" {
		return c
	}
	if !strings.HasPrefix(c, "#") {
		return "#" + c
	}
	return c
}

func (o *Options) Svg() (string, error) {
	// 1. Resolve QR matrix
	level := o.Level
	logoPath := o.Logo.Image
	if logoPath != "" {
		level = goqrcode.High
	}

	qr, err := goqrcode.New(o.Data, level)
	if err != nil {
		return "", err
	}
	qr.DisableBorder = true
	matrix := qr.Bitmap()
	mSize := len(matrix)

	p := o.Padding
	fullSize := mSize + 2*p

	logoSize := o.Logo.Sizing(mSize)

	grid := Analyze(matrix, o)

	svg := NewSVG(fullSize)

	// Draw Background
	o.Background.Size = fullSize
	o.Background.svg(svg)

	// Draw Cells
	o.Cells.svg(svg, grid, p, logoSize > 0)

	// Draw Custom Alignments
	o.Alignment.svg(svg, grid, p)

	// Draw Finders
	o.Finders.svg(svg, mSize, p)

	// Draw Logo
	o.Logo.svg(svg, mSize, p)

	svgStr, err := svg.WriteToString()
	if err != nil {
		return "", err
	}
	return svgStr, nil
}
