package qrcode

import "github.com/skip2/go-qrcode"

type Options struct {
	Data     string
	Template string

	Padding int
	Level   qrcode.RecoveryLevel
	Size    int

	Cells      Cells
	Center     Center
	Finders    Finders
	Background Background

	Alignment Alignment
	Merge     bool `json:"merge,omitempty"`
}

type Stroke struct {
	Size  float64
	Color string
	Dash  Dash
}

type Dash struct {
	Array  float64
	Offset float64
}

type Gradient struct {
	Type   string   `json:"type"`   // "linear" or "radial"
	Colors []string `json:"colors"` // hex gradient colors
	Angle  float64  `json:"angle"`  // linear gradient angle
}

type Background struct {
	Color   string  `json:"color"`
	Rounded float64 `json:"rounded"`
	Stroke  string  `json:"stroke,omitempty"`
	Dashed  string  `json:"dashed,omitempty"` // "cornered", "circular", "full", "none"
	Border  float64 `json:"border,omitempty"` // border thickness
}

type Finders struct {
	TopLeft    Finder `json:"top_left"`
	TopRight   Finder `json:"top_right"`
	BottomLeft Finder `json:"bottom_left"`
}

type Alignment struct {
	Color   string  `json:"color"`
	Stroke  string  `json:"stroke"`
	Rounded float64 `json:"rounded"`
}

type Finder struct {
	Color    string    `json:"color"`
	Stroke   string    `json:"stroke"`
	Rounded  float64   `json:"rounded"`
	Gradient *Gradient `json:"gradient,omitempty"`
}

type Cells struct {
	Active    Cell `json:"active"`
	Passive   Cell `json:"inactive"`
	Center    Cell `json:"center"`
	Alignment Cell `json:"alignment"`
}

type Cell struct {
	Color    string    `json:"color"`
	Size     float64   `json:"size"`
	Rounded  float64   `json:"rounded"`
	Opacity  float64   `json:"opacity"`
	Gradient *Gradient `json:"gradient,omitempty"`
}

type Center struct {
	Logo       string  `json:"logo"`
	Image      string  `json:"image"`
	Background string  `json:"background"`
	Stroke     string  `json:"stroke"`
	Shape      string  `json:"shape"`
	Size       float64 `json:"size"`
	Padding    float64 `json:"padding"`
}
