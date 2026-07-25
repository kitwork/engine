package components

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/jit/css"
)

// GenerateLibrary returns a map of filename -> CSS content for all components.
func GenerateLibrary() map[string]string {
	return map[string]string{
		"tokens.css":  GenerateTokens(),
		"buttons.css": GenerateButtons(),
		"inputs.css":  GenerateInputs(),
		"cards.css":   GenerateCards(),
		"badges.css":  GenerateBadges(),
		"steps.css":   GenerateSteps(),
		"tables.css":  GenerateTables(),
		"alerts.css":  GenerateAlerts(),
		"prose.css":   GenerateProse(),
	}
}

// --- DESIGN TOKENS ---
func GenerateTokens() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: TOKENS */\n")
	b.WriteString(`:root {
	--bg-surface: #121212;
	--text-main: #e0e0e0;
	--text-heading: #ffffff;
	--radius-sm: 4px;
	--radius-md: 6px;
	--radius-lg: 8px;
	--radius-full: 9999px;
	--font-sans: 'Outfit', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
	--glass-bg: rgba(255, 255, 255, 0.03);
	--glass-border: rgba(255, 255, 255, 0.08);
}
@media (prefers-color-scheme: light) {
	:root {
		--bg-surface: #ffffff;
		--text-main: #1f2937;
		--text-heading: #111827;
		--glass-bg: rgba(0, 0, 0, 0.03);
		--glass-border: rgba(0, 0, 0, 0.08);
	}
}
`)
	return b.String()
}

// --- PROSE (Markdown Container) ---
func GenerateProse() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: PROSE (Markdown) */\n")
	b.WriteString(`.prose {
	color: var(--text-main, #e0e0e0);
	max-width: 65ch;
	line-height: 1.7;
	font-family: var(--font-sans, system-ui, -apple-system, sans-serif);
}
.prose h1, .prose h2, .prose h3, .prose h4 {
	color: var(--text-heading, #ffffff);
	font-weight: 700;
	margin-top: 1.5em;
	margin-bottom: 0.5em;
	line-height: 1.3;
}
.prose h1 { font-size: 2em; border-bottom: 1px solid rgba(255,255,255,0.1); padding-bottom: 0.3em; }
.prose h2 { font-size: 1.5em; border-bottom: 1px solid rgba(255,255,255,0.05); padding-bottom: 0.2em; }
.prose h3 { font-size: 1.25em; }
.prose p { margin-top: 0.8em; margin-bottom: 0.8em; }
.prose code {
	background-color: rgba(255,255,255,0.08);
	padding: 0.2em 0.4em;
	border-radius: 4px;
	font-size: 0.9em;
	font-family: monospace;
}
.prose pre {
	background-color: #0d0d0d;
	border: 1px solid rgba(255,255,255,0.1);
	border-radius: 6px;
	padding: 1em;
	overflow-x: auto;
	margin: 1.2em 0;
}
.prose pre code {
	background-color: transparent;
	padding: 0;
}
.prose blockquote {
	border-left: 4px solid rgba(var(--color-brand-rgb, 59, 130, 246), 0.8);
	padding-left: 1em;
	margin: 1em 0;
	color: rgba(255,255,255,0.7);
	font-style: italic;
}
.prose ul, .prose ol { padding-left: 1.5em; margin: 0.8em 0; }
.prose li { margin-bottom: 0.4em; }
.prose img { max-width: 100%; height: auto; border-radius: 6px; }
.prose table { width: 100%; border-collapse: collapse; margin: 1em 0; }
.prose th, .prose td { padding: 8px 12px; border: 1px solid rgba(255,255,255,0.1); }
.prose th { background-color: rgba(255,255,255,0.05); }
`)
	return b.String()
}

// --- BUTTONS ---
func GenerateButtons() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: BUTTONS */\n")

	// Base
	b.WriteString(`.btn { 
	display: inline-flex; align-items: center; justify-content: center; 
	border: 1px solid transparent; border-radius: var(--radius-sm, 4px); 
	font-weight: 600; font-family: var(--font-sans, sans-serif); cursor: pointer; 
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
	background-color: var(--glass-bg, rgba(255,255,255,0.03)); 
	border: 1px solid var(--glass-border, rgba(255,255,255,0.1)); 
	border-radius: var(--radius-sm, 4px); 
	color: var(--text-main, #fff); font-family: var(--font-sans, sans-serif); font-size: 14px;
	transition: border-color 0.2s ease, box-shadow 0.2s ease;
}
.input:focus, .select:focus, .textarea:focus { outline: none; border-color: rgba(var(--color-brand-rgb, 59,130,246), 0.5); box-shadow: 0 0 0 2px rgba(var(--color-brand-rgb, 59,130,246), 0.1); }
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
	b.WriteString(`.card { background-color: var(--bg-surface, #121212); border: 1px solid var(--glass-border, rgba(255,255,255,0.05)); border-radius: var(--radius-lg, 8px); overflow: hidden; display: flex; flex-direction: column; }
.card-header { padding: 20px 24px; border-bottom: 1px solid var(--glass-border, rgba(255,255,255,0.05)); }
.card-body { padding: 24px; flex: 1; }
.card-footer { padding: 16px 24px; background-color: rgba(0,0,0,0.2); border-top: 1px solid var(--glass-border, rgba(255,255,255,0.05)); }
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

	b.WriteString(`.badge { display: inline-flex; align-items: center; padding: 2px 8px; border-radius: var(--radius-full, 9999px); font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.5px; line-height: 1.5; white-space: nowrap; }
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

	b.WriteString(`.alert { padding: 16px; border-radius: var(--radius-md, 6px); border: 1px solid transparent; width: 100%; margin-bottom: 16px; font-size: 14px; }
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
.table th, .table td { padding: 12px 16px; border-bottom: 1px solid var(--glass-border, rgba(255,255,255,0.05)); }
.table th { font-weight: 600; text-transform: uppercase; font-size: 11px; color: rgba(255,255,255,0.5); letter-spacing: 1px; }
.table tbody tr:hover { background-color: rgba(255,255,255,0.02); }
`)
	return b.String()
}

// --- STEPS (Refined) ---
func GenerateSteps() string {
	var b strings.Builder
	b.WriteString("/* Kitwork Industrial Components: STEPS */\n")
	b.WriteString(".steps { display: flex; align-items: center; width: 100%; }\n")
	b.WriteString(".step-item { display: flex; flex-direction: column; align-items: center; flex: 1; position: relative; }\n")
	b.WriteString(".step-item:not(:last-child)::after { content: ''; position: absolute; top: 16px; left: 50%; width: 100%; height: 2px; background-color: rgba(255,255,255,0.1); z-index: 0; }\n")
	b.WriteString(".step-marker { width: 32px; height: 32px; border-radius: 50%; background-color: #121212; border: 2px solid rgba(255,255,255,0.2); display: flex; align-items: center; justify-content: center; z-index: 1; font-weight: bold; font-size: 12px; color: rgba(255,255,255,0.5); transition: all 0.3s ease; }\n")
	b.WriteString(".step-label { margin-top: 12px; font-size: 13px; font-weight: 500; color: rgba(255,255,255,0.4); text-transform: uppercase; letter-spacing: 1px; }\n")

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
	lum := (0.299*float64(bg.R) + 0.587*float64(bg.G) + 0.114*float64(bg.B)) / 255.0
	if lum > 0.6 {
		return "#000000"
	}
	return "#FFFFFF"
}
