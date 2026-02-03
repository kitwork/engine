package components

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/jit/css"
)

// GenerateLibrary returns a map of filename -> CSS content for all components.
func GenerateLibrary() map[string]string {
	return map[string]string{
		"buttons.css": GenerateButtons(),
		"inputs.css":  GenerateInputs(),
		"cards.css":   GenerateCards(),
		"badges.css":  GenerateBadges(),
		"steps.css":   GenerateSteps(),
		"tables.css":  GenerateTables(),
		"alerts.css":  GenerateAlerts(),
	}
}

// --- BUTTONS ---
func GenerateButtons() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: BUTTONS */\n")

	// Base
	b.WriteString(`.btn { 
	display: inline-flex; align-items: center; justify-content: center; 
	border: 1px solid transparent; border-radius: 4px; 
	font-weight: 600; font-family: 'Outfit', sans-serif; cursor: pointer; 
	transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1); 
	outline: none; text-decoration: none; user-select: none;
}
.btn:active { transform: translateY(1px); }
.btn:disabled { opacity: 0.5; cursor: not-allowed; pointer-events: none; }
`)

	// Sizes
	b.WriteString(`.btn-sm { padding: 4px 12px; font-size: 12px; height: 28px; }
.btn-md { padding: 8px 16px; font-size: 14px; height: 36px; }
.btn-lg { padding: 12px 24px; font-size: 16px; height: 48px; }
`)

	// Variants
	for name, color := range css.Colors {
		rgb := fmt.Sprintf("%d, %d, %d", color.R, color.G, color.B)

		// Solid
		b.WriteString(fmt.Sprintf(".btn-%s { background-color: rgb(%s); color: %s; }\n", name, rgb, contrast(color)))
		b.WriteString(fmt.Sprintf(".btn-%s:hover { box-shadow: 0 0 20px rgba(%s, 0.4); }\n", name, rgb))

		// Outline
		b.WriteString(fmt.Sprintf(".btn-outline-%s { background-color: transparent; border-color: rgba(%s, 0.5); color: rgb(%s); }\n", name, rgb, rgb))
		b.WriteString(fmt.Sprintf(".btn-outline-%s:hover { border-color: rgb(%s); background-color: rgba(%s, 0.05); }\n", name, rgb, rgb))

		// Ghost
		b.WriteString(fmt.Sprintf(".btn-ghost-%s { background-color: transparent; color: rgb(%s); }\n", name, rgb))
		b.WriteString(fmt.Sprintf(".btn-ghost-%s:hover { background-color: rgba(%s, 0.1); }\n", name, rgb))
	}

	return b.String()
}

// --- INPUTS ---
func GenerateInputs() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: INPUTS */\n")

	// Base Input
	b.WriteString(`.input, .select, .textarea {
	width: 100%; padding: 10px 16px; 
	background-color: rgba(255,255,255,0.03); 
	border: 1px solid rgba(255,255,255,0.1); 
	border-radius: 4px; 
	color: #fff; font-family: 'Outfit', sans-serif; font-size: 14px;
	transition: border-color 0.2s ease, box-shadow 0.2s ease;
}
.input:focus, .select:focus, .textarea:focus { outline: none; border-color: rgba(var(--color-brand-rgb), 0.5); box-shadow: 0 0 0 2px rgba(var(--color-brand-rgb), 0.1); }
::placeholder { color: rgba(255,255,255,0.3); }
`)

	// Sizes
	b.WriteString(`.input-sm { padding: 6px 12px; font-size: 12px; }
.input-lg { padding: 14px 20px; font-size: 16px; }
`)

	return b.String()
}

// --- CARDS ---
func GenerateCards() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: CARDS */\n")

	// Base
	b.WriteString(`.card { background-color: #121212; border: 1px solid rgba(255,255,255,0.05); border-radius: 8px; overflow: hidden; display: flex; flex-direction: column; }
.card-header { padding: 20px 24px; border-bottom: 1px solid rgba(255,255,255,0.05); }
.card-body { padding: 24px; flex: 1; }
.card-footer { padding: 16px 24px; background-color: rgba(0,0,0,0.2); border-top: 1px solid rgba(255,255,255,0.05); }
`)

	// Variants
	for name, color := range css.Colors {
		rgb := fmt.Sprintf("%d, %d, %d", color.R, color.G, color.B)
		b.WriteString(fmt.Sprintf(".card-%s { border-color: rgba(%s, 0.2); }\n", name, rgb))
		b.WriteString(fmt.Sprintf(".card-%s .card-header { border-bottom-color: rgba(%s, 0.1); color: rgb(%s); }\n", name, rgb, rgb))
	}

	return b.String()
}

// --- BADGES ---
func GenerateBadges() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: BADGES */\n")

	b.WriteString(`.badge { display: inline-flex; align-items: center; padding: 2px 8px; border-radius: 9999px; font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.5px; line-height: 1.5; white-space: nowrap; }
.badge-dot { width: 6px; height: 6px; border-radius: 50%; margin-right: 6px; background-color: currentColor; }
`)

	for name, color := range css.Colors {
		rgb := fmt.Sprintf("%d, %d, %d", color.R, color.G, color.B)
		// Soft style (standard)
		b.WriteString(fmt.Sprintf(".badge-%s { background-color: rgba(%s, 0.1); color: rgb(%s); border: 1px solid rgba(%s, 0.2); }\n", name, rgb, rgb, rgb))
		// Solid style
		b.WriteString(fmt.Sprintf(".badge-solid-%s { background-color: rgb(%s); color: %s; border: none; }\n", name, rgb, contrast(color)))
	}

	return b.String()
}

// --- ALERTS ---
func GenerateAlerts() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: ALERTS */\n")

	b.WriteString(`.alert { padding: 16px; border-radius: 6px; border: 1px solid transparent; width: 100%; margin-bottom: 16px; font-size: 14px; }
`)

	for name, color := range css.Colors {
		rgb := fmt.Sprintf("%d, %d, %d", color.R, color.G, color.B)
		b.WriteString(fmt.Sprintf(".alert-%s { background-color: rgba(%s, 0.08); border-color: rgba(%s, 0.2); color: rgb(%s); }\n", name, rgb, rgb, rgb))
	}
	return b.String()
}

// --- TABLES ---
func GenerateTables() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: TABLES */\n")
	b.WriteString(`.table-container { overflow-x: auto; width: 100%; }
.table { width: 100%; border-collapse: collapse; font-size: 14px; text-align: left; }
.table th, .table td { padding: 12px 16px; border-bottom: 1px solid rgba(255,255,255,0.05); }
.table th { font-weight: 600; text-transform: uppercase; font-size: 11px; color: rgba(255,255,255,0.5); letter-spacing: 1px; }
.table tbody tr:hover { background-color: rgba(255,255,255,0.02); }
`)
	return b.String()
}

// --- STEPS (Refined) ---
func GenerateSteps() string {
	// Re-using the previous logic but made principled if needed.
	// For now, hardcoded is fine as Steps don't usually map 1:1 to all colors unless requested.
	// But we'll add color variants support!
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: STEPS */\n")
	b.WriteString(".steps { display: flex; align-items: center; width: 100%; }\n")
	b.WriteString(".step-item { display: flex; flex-direction: column; align-items: center; flex: 1; position: relative; }\n")
	b.WriteString(".step-item:not(:last-child)::after { content: ''; position: absolute; top: 16px; left: 50%; width: 100%; height: 2px; background-color: rgba(255,255,255,0.1); z-index: 0; }\n")
	b.WriteString(".step-marker { width: 32px; height: 32px; border-radius: 50%; background-color: #121212; border: 2px solid rgba(255,255,255,0.2); display: flex; align-items: center; justify-content: center; z-index: 1; font-weight: bold; font-size: 12px; color: rgba(255,255,255,0.5); transition: all 0.3s ease; }\n")
	b.WriteString(".step-label { margin-top: 12px; font-size: 13px; font-weight: 500; color: rgba(255,255,255,0.4); text-transform: uppercase; letter-spacing: 1px; }\n")

	// Create "Active" variant for Brand by default, but loop for keys
	for name, color := range css.Colors {
		if name == "brand" || name == "success" || name == "primary" {
			rgb := fmt.Sprintf("%d, %d, %d", color.R, color.G, color.B)
			b.WriteString(fmt.Sprintf(".step-item.active-%s:not(:last-child)::after { background-color: rgb(%s); }\n", name, rgb))
			b.WriteString(fmt.Sprintf(".step-item.active-%s .step-marker { border-color: rgb(%s); color: rgb(%s); background-color: rgba(%s, 0.1); box-shadow: 0 0 15px rgba(%s, 0.3); }\n", name, rgb, rgb, rgb, rgb))
			b.WriteString(fmt.Sprintf(".step-item.active-%s .step-label { color: rgb(%s); }\n", name, rgb))
		}
	}
	return b.String()
}

// Helper: Simple contrast checker to decide text color (black or white)
func contrast(bg css.Color) string {
	// Calculate luminance
	lum := (0.299*float64(bg.R) + 0.587*float64(bg.G) + 0.114*float64(bg.B)) / 255.0
	if lum > 0.6 {
		return "#000000"
	}
	return "#FFFFFF"
}
