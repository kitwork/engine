package qrcode

import "math"

type Stroke struct {
	Type  string
	Color string
	Width float32
}

func (s Stroke) formatColor() string {
	stroke := formatColor(s.Color, "#000000")
	if s.Width > 0 {
		stroke = formatColor(s.Color)
	}
	return stroke
}

func (s Stroke) side(size int) float32 {
	return float32(size) - s.Width
}

func (s Stroke) half(size int) float32 {
	return s.side(size) / 2
}

func (s Stroke) perimeter(size int) float32 {
	return 4*s.side(size) - 8*s.Width + 2*math.Pi*s.Width
}

func (s Stroke) DashArray(size int) (result float32) {
	switch s.Type {
	case "cornered", "circular":
		result = s.perimeter(size) / 8
		break
	}
	return
}

func (s Stroke) DashOffset(size int) (result float32) {
	switch s.Type {
	case "cornered":
		result = (s.DashArray(size) / 2) + (s.Width * math.Pi / 4)
		break
	case "circular":
		result = s.DashArray(size) / 2
		break
	}
	return
}
