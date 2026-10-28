package qrcode

import (
	"bytes"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"strings"

	"github.com/fogleman/gg"
	"github.com/nfnt/resize"
	goqrcode "github.com/skip2/go-qrcode"
)

func parseHexColor(hex string) (r, g, b, a int) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		fmt.Sscanf(hex, "%1x%1x%1x", &r, &g, &b)
		r = r * 17
		g = g * 17
		b = b * 17
		a = 255
	} else if len(hex) == 6 {
		fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
		a = 255
	} else if len(hex) == 8 {
		fmt.Sscanf(hex, "%02x%02x%02x%02x", &r, &g, &b, &a)
	} else {
		r, g, b, a = 0, 0, 0, 255
	}
	return
}

func setGGColor(ctx *gg.Context, hex string, opacity float64) {
	r, g, b, a := parseHexColor(hex)
	ctx.SetRGBA255(r, g, b, int(float64(a)*opacity))
}

func (o *Options) getCenterCells(mSize int, logoPath string) int {
	if logoPath == "" {
		return 0
	}
	ratio := o.Logo.Size

	centerCells := int(float64(mSize) * ratio)
	if centerCells > 0 && centerCells%2 == 0 {
		centerCells++ // Centering requires odd number of cells
	}
	if centerCells < 3 {
		centerCells = 3
	}
	if centerCells > mSize-8 {
		centerCells = mSize - 8
		if centerCells%2 == 0 {
			centerCells--
		}
	}
	return centerCells
}

func (o *Options) Png() ([]byte, error) {
	// 1. Resolve QR matrix
	level := o.Level
	logoPath := o.Logo.Image
	if logoPath != "" {
		level = goqrcode.High
	}

	qr, err := goqrcode.New(o.Data, level)
	if err != nil {
		return nil, err
	}
	qr.DisableBorder = true
	matrix := qr.Bitmap()
	mSize := len(matrix)

	p := o.Padding
	canvasCells := mSize + 2*p
	grid := Analyze(matrix, o)
	cellSize := 20.0
	imgSize := canvasCells * int(cellSize)

	ctx := gg.NewContext(imgSize, imgSize)

	// Draw Background
	o.Background.Size = canvasCells
	o.Background.png(ctx, cellSize)

	// Draw Cells
	o.Cells.png(ctx, grid, p, cellSize, o.Template)

	// Draw Custom Alignments
	o.Alignment.png(ctx, grid, p, cellSize, o.Template)

	// Draw Finders
	o.Finders.png(ctx, mSize, p, cellSize, o.Template)

	// Draw Logo
	o.Logo.png(ctx, mSize, p, cellSize)

	// 9. Resize to final output size
	outputSize := o.Size
	if outputSize <= 0 {
		outputSize = 256
	}

	finalImg := resize.Resize(uint(outputSize), uint(outputSize), ctx.Image(), resize.Lanczos3)

	var buf bytes.Buffer
	err = png.Encode(&buf, finalImg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}
