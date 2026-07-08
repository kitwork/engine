package qrcode

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"sort"
	"strings"

	"github.com/fogleman/gg"
	"github.com/nfnt/resize"
)

type Logo struct {
	Image   string  `json:"image"`
	Stroke  string  `json:"stroke"`
	Size    float64 `json:"size"`
	Padding float64 `json:"padding"`
}

func (l *Logo) Sizing(mSize int) float64 {
	if l.Image == "" {
		return 0
	}
	// Lấy kích thước ước lượng ban đầu (khoảng 25% kích thước QR)
	size := int(math.Ceil(float64(mSize) / 4.0))

	// Nếu kích thước là số chẵn, giảm đi 1 để đưa về số lẻ gần nhất
	if size%2 == 0 {
		size--
	}

	// Đảm bảo logo có kích thước tối thiểu là 3x3 ô để nhìn rõ
	if size < 3 {
		size = 3
	}

	l.Size = float64(size)
	return l.Size
}

func (l Logo) svg(svg *Svg, mSize, padding int) {
	image := l.Image
	if image == "" {
		return
	}

	logoSize := float64(l.Size)

	centerStart := (mSize - int(logoSize)) / 2
	logoX := float64(centerStart + padding)
	logoY := float64(centerStart + padding)

	strokeColor := formatColor(l.Stroke)
	hasStroke := l.Stroke != "" && l.Stroke != "transparent" && l.Stroke != "none"

	if hasStroke {
		container := svg.NewElement("rect").
			XY(logoX, logoY).
			Width(logoSize).
			Height(logoSize).
			Fill("none")
		container.Stroke(strokeColor).StrokeWidth(0.15)
	}
	pad := l.Padding
	if pad < 0 {
		pad = 0.0
	}
	imgX := logoX + pad
	imgY := logoY + pad
	imgSize := logoSize - 2.0*pad
	if imgSize <= 0 {
		imgX = logoX
		imgY = logoY
		imgSize = logoSize
	}

	clipID := fmt.Sprintf("center-logo-clip-%d", centerStart)

	defs := svg.NewElement("defs")
	clipPath := defs.New("clipPath").Attribute("id", clipID)

	clipPath.New("rect").
		XY(imgX, imgY).
		Width(imgSize).
		Height(imgSize).
		Rounded(imgSize * 0.1)

	svg.NewElement("image").
		Attribute("href", image).
		XY(imgX, imgY).
		Width(imgSize).
		Height(imgSize).
		Attribute("clip-path", fmt.Sprintf("url(#%s)", clipID))

}

func (l Logo) png(ctx *gg.Context, mSize, padding int, cellSize float64) {
	if l.Image == "" {
		return
	}

	logoSize := float64(l.Size)

	centerStart := (mSize - int(logoSize)) / 2
	logoX := float64(centerStart+padding) * cellSize
	logoY := float64(centerStart+padding) * cellSize
	logoSizeVal := logoSize * cellSize

	strokeColor := formatColor(l.Stroke)
	hasStroke := l.Stroke != "" && l.Stroke != "transparent" && l.Stroke != "none"

	if hasStroke {
		ctx.DrawRoundedRectangle(logoX, logoY, logoSizeVal, logoSizeVal, 0.5*cellSize)
		setGGColor(ctx, strokeColor, 1.0)
		ctx.SetLineWidth(0.15 * cellSize)
		ctx.Stroke()
	}

	logoPath := l.Image
	pad := l.Padding
	if pad < 0 {
		pad = 0.0
	}
	imgX := logoX + pad*cellSize
	imgY := logoY + pad*cellSize
	imgSize := logoSizeVal - 2.0*pad*cellSize
	if imgSize <= 0 {
		imgX = logoX
		imgY = logoY
		imgSize = logoSizeVal
	}

	var imgLogo image.Image
	parts := strings.Split(logoPath, ",")
	base64Data := parts[len(parts)-1]
	if decoded, err := base64.StdEncoding.DecodeString(base64Data); err == nil {
		if img, _, err := image.Decode(bytes.NewReader(decoded)); err == nil {
			imgLogo = img
		}
	}

	if imgLogo != nil {
		logoResized := resize.Resize(uint(imgSize), uint(imgSize), imgLogo, resize.Lanczos3)
		ctx.Push()
		ctx.DrawRoundedRectangle(imgX, imgY, imgSize, imgSize, imgSize*0.1)
		ctx.Clip()
		ctx.DrawImage(logoResized, int(imgX), int(imgY))
		ctx.Pop()
	}
}

func (o *Options) ExtractLogoColors() ([]string, error) {
	logoPath := o.Logo.Image
	if logoPath == "" {
		return nil, fmt.Errorf("no logo image configured")
	}

	parts := strings.Split(logoPath, ",")
	base64Data := parts[len(parts)-1]
	decoded, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 logo: %w", err)
	}

	img, _, err := image.Decode(bytes.NewReader(decoded))
	if err != nil {
		return nil, fmt.Errorf("failed to decode logo image: %w", err)
	}

	colorCount := make(map[string]int)
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixelColor := img.At(x, y)
			r, g, b, a := pixelColor.RGBA()

			// Skip transparent or near-transparent pixels (a < 1000 out of 65535)
			if a < 1000 {
				continue
			}

			// Convert to standard 8-bit RGBA
			rgbaColor := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}

			// Skip near-white colors (threshold 225)
			if rgbaColor.R >= 225 && rgbaColor.G >= 225 && rgbaColor.B >= 225 {
				continue
			}

			hexColor := fmt.Sprintf("#%02x%02x%02x", rgbaColor.R, rgbaColor.G, rgbaColor.B)
			colorCount[hexColor]++
		}
	}

	type colorItem struct {
		Color string
		Count int
	}
	var list []colorItem
	for col, count := range colorCount {
		list = append(list, colorItem{Color: col, Count: count})
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Count > list[j].Count
	})

	var result []string
	for _, item := range list {
		result = append(result, item.Color)
	}

	return result, nil
}
