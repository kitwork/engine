package qrcode

import "github.com/skip2/go-qrcode"

type Options struct {
	Level    qrcode.RecoveryLevel
	Data     string
	Template string

	Size    int
	Padding int
	Merge   bool

	Cells Cells

	Logo       Logo
	Finders    Finders
	Background Background

	Alignment Alignment

	grid [][]Module
}
