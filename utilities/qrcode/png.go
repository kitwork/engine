package qrcode

import (
	"bytes"
	"fmt"
	"image/png"
	"strings"

	"github.com/nfnt/resize"
	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
)

func (o *Options) Png() ([]byte, error) {
	svgStr, err := o.Svg()
	if err != nil {
		return nil, err
	}

	reader := strings.NewReader(svgStr)
	c, err := canvas.ParseSVG(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SVG: %w", err)
	}

	// 1. Draw SVG to high quality canvas (12 DPMM creates high density pixels)
	img := rasterizer.Draw(c, canvas.DPMM(12.0), canvas.DefaultColorSpace)

	// 2. Resize to requested exact output size
	outputSize := o.Size
	if outputSize <= 0 {
		outputSize = 256
	}
	resizedImg := resize.Resize(uint(outputSize), uint(outputSize), img, resize.Lanczos3)

	var buf bytes.Buffer
	err = png.Encode(&buf, resizedImg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}
