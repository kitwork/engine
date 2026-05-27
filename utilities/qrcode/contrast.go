package qrcode

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func parseHex(hex string) (r, g, b float64) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, 0, 0
	}
	val, _ := strconv.ParseInt(hex, 16, 64)
	return float64((val >> 16) & 0xFF), float64((val >> 8) & 0xFF), float64(val & 0xFF)
}

func relativeLuminance(r, g, b float64) float64 {
	rs := r / 255.0
	gs := g / 255.0
	bs := b / 255.0

	var rVal, gVal, bVal float64
	if rs <= 0.03928 {
		rVal = rs / 12.92
	} else {
		rVal = math.Pow((rs+0.055)/1.055, 2.4)
	}

	if gs <= 0.03928 {
		gVal = gs / 12.92
	} else {
		gVal = math.Pow((gs+0.055)/1.055, 2.4)
	}

	if bs <= 0.03928 {
		bVal = bs / 12.92
	} else {
		bVal = math.Pow((bs+0.055)/1.055, 2.4)
	}

	return 0.2126*rVal + 0.7152*gVal + 0.0722*bVal
}

func contrastRatio(lum1, lum2 float64) float64 {
	if lum1 > lum2 {
		return (lum1 + 0.05) / (lum2 + 0.05)
	}
	return (lum2 + 0.05) / (lum1 + 0.05)
}

func ensureContrast(cellColorHex, bgColorHex string) string {
	if bgColorHex == "" || bgColorHex == "transparent" || bgColorHex == "none" {
		bgColorHex = "#ffffff"
	}
	rC, gC, bC := parseHex(cellColorHex)
	rB, gB, bB := parseHex(bgColorHex)

	lumB := relativeLuminance(rB, gB, bB)

	// Smart luminance contrast adjust loop
	for i := 0; i < 10; i++ {
		lumC := relativeLuminance(rC, gC, bC)
		ratio := contrastRatio(lumC, lumB)
		if ratio >= 3.0 {
			return fmt.Sprintf("#%02x%02x%02x", uint8(rC), uint8(gC), uint8(bC))
		}

		if lumB > 0.5 {
			// Light background: make brand color darker
			rC = math.Max(0, rC*0.8)
			gC = math.Max(0, gC*0.8)
			bC = math.Max(0, bC*0.8)
		} else {
			// Dark background: make brand color lighter
			rC = math.Min(255, rC+(255-rC)*0.2)
			gC = math.Min(255, gC+(255-gC)*0.2)
			bC = math.Min(255, bC+(255-bC)*0.2)
		}
	}

	if lumB > 0.5 {
		return "#000000"
	} else {
		return "#ffffff"
	}
}
