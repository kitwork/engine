package qrcode

import (
	"fmt"
	"math"
	"strings"

	goqrcode "github.com/skip2/go-qrcode"
)

// Cell shapes normalized to a 1x1 coordinate system
var cellShapes = map[string]string{
	"heart":   "M0.75 0C0.55 0 0.5 0.166667 0.5 0.25C0.5 0.166667 0.45 0 0.25 0C0.05 0 0 0.166667 0 0.25C0 0.416667 0 0.5 0.5 1C1 0.5 1 0.416667 1 0.25C1 0.166667 0.95 0 0.75 0Z",
	"diamond": "M1 0.5C0.75 0.5 0.5 0.25 0.5 0C0.5 0.25 0.25 0.5 0 0.5C0.25 0.5 0.5 0.75 0.5 1C0.5 0.75 0.75 0.5 1 0.5Z",
	"club":    "M0.5 0C0.583333 0 0.75 0.05 0.75 0.25C0.95 0.25 1 0.416667 1 0.5C1 0.583333 0.95 0.75 0.75 0.75C0.55 0.75 0.5 0.583333 0.5 0.5C0.5 0.9 0.666667 1 0.75 1H0.25C0.45 1 0.5 0.666667 0.5 0.5C0.5 0.7 0.333333 0.75 0.25 0.75C0.166667 0.75 0 0.7 0 0.5C0 0.3 0.166667 0.25 0.25 0.25C0.25 0.05 0.416667 0 0.5 0Z",
	"spade":   "M0.25 0.75C0.166667 0.75 0 0.7 0 0.5C0 0.25 0.25 0.25 0.5 0C0.75 0.25 1 0.3 1 0.5C1 0.583333 0.95 0.75 0.75 0.75C0.55 0.75 0.5 0.583333 0.5 0.5C0.5 0.9 0.666667 1 0.75 1H0.25C0.45 1 0.5 0.666667 0.5 0.5C0.5 0.7 0.333333 0.75 0.25 0.75Z",
	"petal":   "M0.5 0 A0.5 0.5 0 1 0 1 0.5 A0.5 0.5 0 1 0 0.5 0 Z",
	"square":  "M0 0 L1 0 L1 1 L0 1 Z",
	"circle":  "M0 0.5 A0.5 0.5 0 0 1 0.5 0 A0.5 0.5 0 0 1 1 0.5 A0.5 0.5 0 0 1 0.5 1 A0.5 0.5 0 0 1 0 0.5 Z",
}

func formatColor(c string) string {
	if c == "" {
		return "#000000"
	}
	if c == "transparent" || c == "none" {
		return c
	}
	if !strings.HasPrefix(c, "#") {
		return "#" + c
	}
	return c
}

func (bg Background) Side(canvasSize float64) float64 {
	return canvasSize - bg.Border
}

func (bg Background) Perimeter(canvasSize float64) float64 {
	return 4*bg.Side(canvasSize) - 8*bg.Rounded + 2*math.Pi*bg.Rounded
}

func (bg Background) DashArray(canvasSize float64) float64 {
	if bg.Dashed == "cornered" || bg.Dashed == "circular" {
		return bg.Perimeter(canvasSize) / 8.0
	}
	return 0
}

func (bg Background) DashOffset(canvasSize float64) float64 {
	if bg.Dashed == "cornered" {
		return (bg.DashArray(canvasSize) / 2.0) + (bg.Rounded * math.Pi / 4.0)
	}
	if bg.Dashed == "circular" {
		return bg.DashArray(canvasSize) / 2.0
	}
	return 0
}

func writeGradientDefs(sb *strings.Builder, id string, grad *Gradient) {
	if grad == nil || len(grad.Colors) < 2 {
		return
	}
	if grad.Type == "linear" {
		angleRad := grad.Angle * math.Pi / 180.0
		x1 := 50.0 - 50.0*math.Cos(angleRad)
		y1 := 50.0 + 50.0*math.Sin(angleRad)
		x2 := 50.0 + 50.0*math.Cos(angleRad)
		y2 := 50.0 - 50.0*math.Sin(angleRad)
		sb.WriteString(fmt.Sprintf(`<linearGradient id="%s" x1="%.1f%%" y1="%.1f%%" x2="%.1f%%" y2="%.1f%%">`, id, x1, y1, x2, y2))
	} else {
		sb.WriteString(fmt.Sprintf(`<radialGradient id="%s" cx="50%%" cy="50%%" r="50%%">`, id))
	}

	numColors := len(grad.Colors)
	for idx, color := range grad.Colors {
		offset := float64(idx) / float64(numColors-1) * 100.0
		sb.WriteString(fmt.Sprintf(`<stop offset="%.1f%%" stop-color="%s" />`, offset, formatColor(color)))
	}

	if grad.Type == "linear" {
		sb.WriteString(`</linearGradient>`)
	} else {
		sb.WriteString(`</radialGradient>`)
	}
}

func (o *Options) Svg() (string, error) {
	// 1. Resolve QR matrix
	level := o.Level
	logoPath := o.Center.Image
	if logoPath == "" {
		logoPath = o.Center.Logo
	}
	if logoPath != "" {
		level = goqrcode.High // Force high level for logo to prevent scan errors
	}

	qr, err := goqrcode.New(o.Data, level)
	if err != nil {
		return "", err
	}
	qr.DisableBorder = true
	matrix := qr.Bitmap()
	mSize := len(matrix)

	p := o.Padding
	canvasSize := mSize + 2*p

	// 2. Resolve Dominant Logo Color if needed
	var domColor string
	getDomColor := func() string {
		if domColor == "" {
			colors, err := o.ExtractLogoColors()
			if err == nil && len(colors) > 0 {
				domColor = colors[0]
			} else {
				domColor = "#000000"
			}
		}
		return domColor
	}

	resolveColor := func(c string) string {
		if strings.ToLower(c) == "auto" {
			return getDomColor()
		}
		return formatColor(c)
	}

	// 3. Resolve Background and Container Fill Styles
	bgColor := resolveColor(o.Background.Color)
	if bgColor == "" {
		bgColor = "#ffffff"
	}

	activeColor := resolveColor(o.Cells.Active.Color)
	if o.Cells.Active.Gradient == nil || len(o.Cells.Active.Gradient.Colors) < 2 {
		activeColor = ensureContrast(activeColor, bgColor)
	} else {
		activeColor = "url(#cell-gradient)"
	}

	passiveColor := resolveColor(o.Cells.Passive.Color)
	alignmentColor := resolveColor(o.Alignment.Color)
	if alignmentColor == "" {
		alignmentColor = resolveColor(o.Cells.Alignment.Color)
	}
	alignmentStroke := resolveColor(o.Alignment.Stroke)
	if alignmentStroke == "" {
		alignmentStroke = alignmentColor
	}
	alRounded := o.Alignment.Rounded
	if alRounded <= 0 {
		alRounded = o.Cells.Alignment.Rounded
	}

	// 4. Center Area Dimensions
	centerCells := 0
	if logoPath != "" || o.Center.Background != "" {
		ratio := o.Center.Size
		if ratio <= 0 {
			ratio = 0.2
		}
		centerCells = int(float64(mSize) * ratio)
		if centerCells > 0 && centerCells%2 == 0 {
			centerCells++ // Perfect centering requires odd number of cells
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
	}

	isCenter := func(x, y int) bool {
		if centerCells <= 0 {
			return false
		}
		centerStart := (mSize - centerCells) / 2
		centerEnd := centerStart + centerCells
		return x >= centerStart && x < centerEnd && y >= centerStart && y < centerEnd
	}

	// 5. Finder coordinates filter
	isFinder := func(x, y int) bool {
		return (x >= 0 && x < 7 && y >= 0 && y < 7) ||
			(x >= 0 && x < 7 && y >= mSize-7 && y < mSize) ||
			(x >= mSize-7 && x < mSize && y >= 0 && y < 7)
	}

	// 6. Custom Alignment coordinates filter
	hasCustomAlignment := alignmentColor != "" || alignmentStroke != ""
	alignmentCenters := getAlignmentPatternPositions(qr.VersionNumber)

	isAlignmentRegion := func(x, y int) bool {
		if !hasCustomAlignment {
			return false
		}
		for _, cx := range alignmentCenters {
			for _, cy := range alignmentCenters {
				// Skip finders regions
				if (cx == 6 && cy == 6) || (cx == 6 && cy == mSize-7) || (cx == mSize-7 && cy == 6) {
					continue
				}
				if x >= cx-2 && x <= cx+2 && y >= cy-2 && y <= cy+2 {
					return true
				}
			}
		}
		return false
	}

	// Start building SVG
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<svg viewBox="0 0 %d %d" version="1.1" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" style="width:100%%;height:100%%;">`, canvasSize, canvasSize))

	// Define Gradients in SVG Defs
	hasGradients := (o.Cells.Active.Gradient != nil && len(o.Cells.Active.Gradient.Colors) >= 2) ||
		(o.Finders.TopLeft.Gradient != nil && len(o.Finders.TopLeft.Gradient.Colors) >= 2) ||
		(o.Finders.TopRight.Gradient != nil && len(o.Finders.TopRight.Gradient.Colors) >= 2) ||
		(o.Finders.BottomLeft.Gradient != nil && len(o.Finders.BottomLeft.Gradient.Colors) >= 2)

	if hasGradients {
		sb.WriteString("<defs>")
		writeGradientDefs(&sb, "cell-gradient", o.Cells.Active.Gradient)
		writeGradientDefs(&sb, "finder-tl-gradient", o.Finders.TopLeft.Gradient)
		writeGradientDefs(&sb, "finder-tr-gradient", o.Finders.TopRight.Gradient)
		writeGradientDefs(&sb, "finder-bl-gradient", o.Finders.BottomLeft.Gradient)
		sb.WriteString("</defs>")
	}

	// Draw background
	if bgColor != "transparent" && bgColor != "none" {
		if o.Background.Rounded > 0 {
			sb.WriteString(fmt.Sprintf(`<rect width="%d" height="%d" fill="%s" rx="%.2f" />`, canvasSize, canvasSize, bgColor, o.Background.Rounded))
		} else {
			sb.WriteString(fmt.Sprintf(`<rect width="%d" height="%d" fill="%s" />`, canvasSize, canvasSize, bgColor))
		}
	}

	// Draw Background Border (including dashed corner/circular patterns)
	bgStroke := resolveColor(o.Background.Stroke)
	if bgStroke != "" && bgStroke != "transparent" && bgStroke != "none" && o.Background.Border > 0 {
		border := o.Background.Border
		rx := o.Background.Rounded
		if o.Background.Dashed == "circular" {
			rx = (float64(canvasSize) - border) / 2.0
		}
		dashAttr := ""
		if o.Background.Dashed == "cornered" || o.Background.Dashed == "circular" {
			dashAttr = fmt.Sprintf(` stroke-dasharray="%.2f" stroke-dashoffset="%.2f"`,
				o.Background.DashArray(float64(canvasSize)), o.Background.DashOffset(float64(canvasSize)))
		}
		sb.WriteString(fmt.Sprintf(`<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="none" stroke="%s" stroke-width="%.2f" rx="%.2f"%s />`,
			border/2.0, border/2.0, float64(canvasSize)-border, float64(canvasSize)-border, bgStroke, border, rx, dashAttr))
	}

	// Helper to draw cells using normalized shape templates individually
	drawIndividualCell := func(cx, cy, size, rx float64, fill string, opacity float64) {
		template := o.Template
		if template == "circle" || template == "circular" || template == "dot" {
			sb.WriteString(fmt.Sprintf(`<circle cx="%.2f" cy="%.2f" r="%.2f" fill="%s" fill-opacity="%.2f" />`,
				cx+0.5, cy+0.5, size*0.5, fill, opacity))
			return
		}
		if template == "square" || template == "rounded" || template == "" {
			offset := (1.0 - size) / 2.0
			sb.WriteString(fmt.Sprintf(`<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f" fill="%s" fill-opacity="%.2f" />`,
				cx+offset, cy+offset, size, size, rx, fill, opacity))
			return
		}

		pathData, exists := cellShapes[template]
		if !exists {
			pathData = cellShapes["square"]
		}
		offset := (1.0 - size) / 2.0
		sb.WriteString(fmt.Sprintf(`<path d="%s" transform="translate(%.2f, %.2f) scale(%.2f)" fill="%s" fill-opacity="%.2f" />`,
			pathData, cx+offset, cy+offset, size, fill, opacity))
	}

	// Helper to append cell paths for mergeable templates
	appendCellPath := func(pathSb *strings.Builder, template string, cx, cy, size, rx float64) bool {
		if template == "circle" || template == "circular" || template == "dot" {
			R := size * 0.5
			cX := cx + 0.5
			cY := cy + 0.5
			pathSb.WriteString(fmt.Sprintf("M%.2f %.2f A%.2f %.2f 0 1 0 %.2f %.2f A%.2f %.2f 0 1 0 %.2f %.2f Z ",
				cX-R, cY, R, R, cX+R, cY, R, R, cX-R, cY))
			return true
		}
		if template == "square" || template == "rounded" || template == "" {
			if template == "rounded" || rx > 0 {
				offset := (1.0 - size) / 2.0
				xVal := cx + offset
				yVal := cy + offset
				if rx <= 0 {
					rx = 0.2
				}
				if rx > size*0.5 {
					rx = size * 0.5
				}
				pathSb.WriteString(fmt.Sprintf("M%.2f %.2f h%.2f a%.2f %.2f 0 0 1 %.2f %.2f v%.2f a%.2f %.2f 0 0 1 %.2f %.2f h-%.2f a%.2f %.2f 0 0 1 %.2f %.2f v-%.2f a%.2f %.2f 0 0 1 %.2f %.2f Z ",
					xVal+rx, yVal,
					size-2*rx,
					rx, rx, rx, rx,
					size-2*rx,
					rx, rx, -rx, rx,
					size-2*rx,
					rx, rx, -rx, -rx,
					size-2*rx,
					rx, rx, rx, -rx))
				return true
			}
			offset := (1.0 - size) / 2.0
			xVal := cx + offset
			yVal := cy + offset
			pathSb.WriteString(fmt.Sprintf("M%.2f %.2f h%.2f v%.2f h-%.2f Z ",
				xVal, yVal, size, size, size))
			return true
		}
		return false
	}

	// Active Cell configurations
	activeSize := o.Cells.Active.Size
	if activeSize <= 0 {
		activeSize = 1.0
	}
	activeOpacity := o.Cells.Active.Opacity
	if activeOpacity <= 0 {
		activeOpacity = 1.0
	}
	activeRx := o.Cells.Active.Rounded

	// Passive Cell configurations
	passiveSize := o.Cells.Passive.Size
	if passiveSize <= 0 {
		passiveSize = 0.85
	}
	passiveOpacity := o.Cells.Passive.Opacity
	if passiveOpacity <= 0 {
		passiveOpacity = 0.3
	}
	passiveRx := o.Cells.Passive.Rounded

	var activePaths strings.Builder
	var passivePaths strings.Builder

	// Draw Cells (Data and Passive)
	for y := 0; y < mSize; y++ {
		for x := 0; x < mSize; x++ {
			if isFinder(x, y) || isCenter(x, y) || isAlignmentRegion(x, y) {
				continue
			}

			cx := float64(x + p)
			cy := float64(y + p)

			if matrix[y][x] {
				if !o.Merge || !appendCellPath(&activePaths, o.Template, cx, cy, activeSize, activeRx) {
					drawIndividualCell(cx, cy, activeSize, activeRx, activeColor, activeOpacity)
				}
			} else if passiveColor != "" && passiveColor != "transparent" && passiveColor != "none" {
				if !o.Merge || !appendCellPath(&passivePaths, o.Template, cx, cy, passiveSize, passiveRx) {
					drawIndividualCell(cx, cy, passiveSize, passiveRx, passiveColor, passiveOpacity)
				}
			}
		}
	}

	// Write merged paths
	if activePaths.Len() > 0 {
		sb.WriteString(fmt.Sprintf(`<path d="%s" fill="%s" fill-opacity="%.2f" />`,
			strings.TrimSpace(activePaths.String()), activeColor, activeOpacity))
	}
	if passivePaths.Len() > 0 {
		sb.WriteString(fmt.Sprintf(`<path d="%s" fill="%s" fill-opacity="%.2f" />`,
			strings.TrimSpace(passivePaths.String()), passiveColor, passiveOpacity))
	}

	// Draw Custom Alignments
	if hasCustomAlignment {
		var outerPaths strings.Builder
		var innerPaths strings.Builder

		for _, cx := range alignmentCenters {
			for _, cy := range alignmentCenters {
				// Skip finders regions
				if (cx == 6 && cy == 6) || (cx == 6 && cy == mSize-7) || (cx == mSize-7 && cy == 6) {
					continue
				}

				X := float64(cx - 2 + p)
				Y := float64(cy - 2 + p)
				alRoundVal := alRounded
				if alRoundVal <= 0 {
					alRoundVal = 1.0
				}

				// Outer rect (4x4)
				xVal := X + 0.5
				yVal := Y + 0.5
				S := 4.0
				r := alRoundVal
				if r > S*0.5 {
					r = S * 0.5
				}
				outerPaths.WriteString(fmt.Sprintf("M%.2f %.2f h%.2f a%.2f %.2f 0 0 1 %.2f %.2f v%.2f a%.2f %.2f 0 0 1 %.2f %.2f h-%.2f a%.2f %.2f 0 0 1 %.2f %.2f v-%.2f a%.2f %.2f 0 0 1 %.2f %.2f Z ",
					xVal+r, yVal,
					S-2*r,
					r, r, r, r,
					S-2*r,
					r, r, -r, r,
					S-2*r,
					r, r, -r, -r,
					S-2*r,
					r, r, r, -r))

				// Inner dot (1x1)
				xVal2 := X + 2.0
				yVal2 := Y + 2.0
				S2 := 1.0
				r2 := alRoundVal / 2.0
				if r2 > S2*0.5 {
					r2 = S2 * 0.5
				}
				innerPaths.WriteString(fmt.Sprintf("M%.2f %.2f h%.2f a%.2f %.2f 0 0 1 %.2f %.2f v%.2f a%.2f %.2f 0 0 1 %.2f %.2f h-%.2f a%.2f %.2f 0 0 1 %.2f %.2f v-%.2f a%.2f %.2f 0 0 1 %.2f %.2f Z ",
					xVal2+r2, yVal2,
					S2-2*r2,
					r2, r2, r2, r2,
					S2-2*r2,
					r2, r2, -r2, r2,
					S2-2*r2,
					r2, r2, -r2, -r2,
					S2-2*r2,
					r2, r2, r2, -r2))
			}
		}

		if outerPaths.Len() > 0 {
			sb.WriteString(fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="1" />`,
				strings.TrimSpace(outerPaths.String()), alignmentStroke))
		}
		if innerPaths.Len() > 0 {
			sb.WriteString(fmt.Sprintf(`<path d="%s" fill="%s" />`,
				strings.TrimSpace(innerPaths.String()), alignmentColor))
		}
	}

	// Draw Finders (styled independently with fallback)
	drawFinder := func(fx, fy int, f Finder, gradId string) {
		X := float64(fx + p)
		Y := float64(fy + p)

		var resolvedColor, resolvedStroke string
		if f.Gradient != nil && len(f.Gradient.Colors) >= 2 {
			resolvedColor = fmt.Sprintf("url(#%s)", gradId)
			resolvedStroke = fmt.Sprintf("url(#%s)", gradId)
		} else {
			fColor := f.Color
			if fColor == "" {
				fColor = o.Cells.Active.Color
			}
			fStroke := f.Stroke
			if fStroke == "" {
				fStroke = o.Cells.Active.Color
			}
			resolvedColor = ensureContrast(resolveColor(fColor), bgColor)
			resolvedStroke = ensureContrast(resolveColor(fStroke), bgColor)
		}

		fRounded := f.Rounded
		if fRounded <= 0 {
			fRounded = 2.0
		}

		// Outer Frame (7x7) -> rendered as width 6 x height 6
		sb.WriteString(fmt.Sprintf(`<rect x="%.2f" y="%.2f" width="6" height="6" fill="none" stroke="%s" stroke-width="1" rx="%.2f" />`,
			X+0.5, Y+0.5, resolvedStroke, fRounded))

		// Inner Dot (3x3)
		sb.WriteString(fmt.Sprintf(`<rect x="%.2f" y="%.2f" width="3" height="3" fill="%s" rx="%.2f" />`,
			X+2, Y+2, resolvedColor, fRounded/2.0))
	}

	drawFinder(0, 0, o.Finders.TopLeft, "finder-tl-gradient")
	drawFinder(mSize-7, 0, o.Finders.TopRight, "finder-tr-gradient")
	drawFinder(0, mSize-7, o.Finders.BottomLeft, "finder-bl-gradient")

	// Draw Center Logo / Background
	if centerCells > 0 {
		centerStart := (mSize - centerCells) / 2
		logoX := float64(centerStart + p)
		logoY := float64(centerStart + p)
		logoSize := float64(centerCells)

		// Draw Center background block if configured (includes optional center stroke border)
		bgFill := resolveColor(o.Center.Background)
		strokeColor := resolveColor(o.Center.Stroke)
		strokeAttr := ""
		if strokeColor != "" && strokeColor != "transparent" && strokeColor != "none" {
			strokeAttr = fmt.Sprintf(` stroke="%s" stroke-width="0.15"`, strokeColor)
		}

		if (bgFill != "" && bgFill != "transparent" && bgFill != "none") || strokeAttr != "" {
			if bgFill == "" {
				bgFill = "none"
			}
			if o.Center.Shape == "circle" {
				radius := logoSize / 2.0
				sb.WriteString(fmt.Sprintf(`<circle cx="%.2f" cy="%.2f" r="%.2f" fill="%s"%s />`,
					logoX+radius, logoY+radius, radius, bgFill, strokeAttr))
			} else {
				sb.WriteString(fmt.Sprintf(`<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s" rx="0.5"%s />`,
					logoX, logoY, logoSize, logoSize, bgFill, strokeAttr))
			}
		}

		if logoPath != "" {
			pad := o.Center.Padding
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

			if logoPath == "vietqr" {
				scaleFactor := imgSize / 10.0
				sb.WriteString(fmt.Sprintf(`<g transform="translate(%.2f, %.2f) scale(%.4f)">
					<rect x="0" y="0" width="10" height="10" fill="#005ba1" rx="2" />
					<path d="M2.5 3.5 L5 7 L7.5 3.5" fill="none" stroke="#ffffff" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round" />
					<circle cx="5" cy="5" r="4.2" fill="none" stroke="#ff5f56" stroke-width="0.6" stroke-dasharray="2 1.5" />
				</g>`, imgX, imgY, scaleFactor))
			} else {
				base64Image := getBase64Image(logoPath)
				if base64Image != "" {
					// Use defs and clipPath to crop the image matching the Shape
					clipID := fmt.Sprintf("center-logo-clip-%d", centerStart)
					sb.WriteString(fmt.Sprintf("<defs><clipPath id=\"%s\">", clipID))
					if o.Center.Shape == "circle" {
						sb.WriteString(fmt.Sprintf(`<circle cx="%.2f" cy="%.2f" r="%.2f" />`, imgX+imgSize/2, imgY+imgSize/2, imgSize/2))
					} else {
						// Rounded rect for logo image
						sb.WriteString(fmt.Sprintf(`<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f" />`, imgX, imgY, imgSize, imgSize, imgSize*0.1))
					}
					sb.WriteString("</clipPath></defs>")

					// Draw the image
					sb.WriteString(fmt.Sprintf(`<image href="%s" x="%.2f" y="%.2f" width="%.2f" height="%.2f" clip-path="url(#%s)" />`,
						base64Image, imgX, imgY, imgSize, imgSize, clipID))
				}
			}
		}
	}

	sb.WriteString("</svg>")
	return sb.String(), nil
}
