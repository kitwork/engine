package css

import (
	"fmt"
	"strconv"
	"strings"
)

// --- CORE CONFIGURATION & CONSTANTS ---

const ExplicitUnit = "px"

type Color struct{ R, G, B int }

func (c Color) String() string { return fmt.Sprintf("%d, %d, %d", c.R, c.G, c.B) }
func Hex(h string) Color {
	h = strings.TrimPrefix(h, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	val, _ := strconv.ParseUint(h, 16, 32)
	return Color{int(val >> 16), int((val >> 8) & 0xFF), int(val & 0xFF)}
}

type Config struct {
	Colors       map[string]Color
	Order        []string
	MediaQueries map[string]string
	States       map[string]string
	ShadowLevels map[string]string
	Scale        []int
	AlphaScales  []int
	Opacities    []int // 0-100 scale
	ZIndices     []int // 0, 10, 20...
}

var DefaultConfig = Config{
	Colors: map[string]Color{
		"white":       Hex("#FFFFFF"),
		"black":       Hex("#000000"),
		"gray":        Hex("#808080"),
		"red":         Hex("#FF0000"),
		"deeppink":    Hex("#FF1493"),
		"orangered":   Hex("#FF4500"),
		"gold":        Hex("#FFD900"),
		"darkviolet":  Hex("#9400D3"),
		"lime":        Hex("#00FF00"),
		"deepskyblue": Hex("#00BFFF"),
		"peace":       Hex("#8866FF"),
		"blue":        Hex("#4242FF"),
		"primary":     Hex("#4242FF"),
		"warning":     Hex("#FF6824"),
		"positive":    Hex("#8824FF"),
		"danger":      Hex("#FF4242"),
		"success":     Hex("#02D842"),
		"info":        Hex("#2288FF"),
		"default":     Hex("#E2E8F6"),
		"special":     Hex("#240224"),
		"dark":        Hex("#121212"),
		"elegant":     Hex("#242424"),
		"stylish":     Hex("#242442"),
		"unique":      Hex("#022442"),
		"profession":  Hex("#16161B"),
		"light":       Hex("#F2F4F6"),
		"ghost":       Hex("#F8F8FF"),
		"lavender":    Hex("#E6E6FA"),
		"kitwork":     Hex("#f82244"),
		"brand":       Hex("#f82244"),
	},

	Order: []string{"white", "black", "gray", "red", "deeppink", "orangered", "gold", "darkviolet", "lime", "deepskyblue", "peace", "blue", "primary", "warning", "positive", "danger", "success", "info", "special", "dark", "elegant", "stylish", "unique", "profession", "light", "ghost", "lavender", "default", "kitwork", "brand"},

	MediaQueries: map[string]string{
		"mobile":  "@media (max-width: 600px)",
		"tablet":  "@media (max-width: 900px)",
		"laptop":  "@media (max-width: 1200px)",
		"desktop": "@media (min-width: 1280px)",
	},

	States: map[string]string{
		"hover":       "hover",
		"group-hover": ".group:hover &",
		"focus":       "focus",
		"active":      "active",
		"disabled":    "disabled",
		"visited":     "visited",
		"first":       "first-child",
		"last":        "last-child",
	},

	ShadowLevels: map[string]string{
		"small":      "0 1px 2px rgba(0,0,0,0.1)",
		"medium":     "0 4px 6px rgba(0,0,0,0.1)",
		"large":      "0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05)",
		"giant":      "0 20px 25px rgba(0,0,0,0.15)",
		"wide":       "0 25px 50px -12px rgba(0, 0, 0, 0.25)",
		"glow":       "0 0 40px rgba(248, 34, 68, 0.15)",
		"glow-brand": "0 0 30px rgba(248, 34, 68, 0.4)",
		"industrial": "0 10px 30px -10px rgba(0,0,0,0.5)",
		"system":     "0 0 0 1px rgba(255, 255, 255, 0.05), 0 20px 40px -12px rgba(0,0,0,0.8)",
		"core":       "0 0 0 1px rgba(248, 34, 68, 0.1), 0 20px 40px -12px rgba(248, 34, 68, 0.1)",
		"great":      "0 30px 60px -12px rgba(0,0,0,0.6)",
		"none":       "none",
	},

	Scale: []int{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
		11, 12, 14, 16, 18, 20, 24, 28, 30, 32,
		36, 40, 48, 50, 56, 60, 64, 72, 80, 96,
		100, 120, 128, 140, 160, 180, 200, 240,
		320, 360, 400, 480,
	},
	AlphaScales: []int{2, 5, 8, 10, 20, 30, 40, 50, 60, 80},
	Opacities:   []int{0, 5, 10, 20, 25, 30, 40, 50, 60, 70, 75, 80, 90, 95, 100},
	ZIndices:    []int{0, 10, 20, 30, 40, 50, 100, 999, 9999},
}

var (
	Colors       = DefaultConfig.Colors
	Order        = DefaultConfig.Order
	MediaQueries = DefaultConfig.MediaQueries
	States       = DefaultConfig.States
	ShadowLevels = DefaultConfig.ShadowLevels
	Scale        = DefaultConfig.Scale
	AlphaScales  = DefaultConfig.AlphaScales
)
