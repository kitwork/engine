package main

import (
	"fmt"
	"io/ioutil"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	inputPath := "demo/view/work.html"
	content, err := ioutil.ReadFile(inputPath)
	if err != nil {
		fmt.Printf("Error reading input file (%s): %v\n", inputPath, err)
		return
	}
	htmlContent := string(content)

	fmt.Printf("Analyzing %s...\n", inputPath)

	css := GenerateUltimateJITCSS(htmlContent)

	outputPath := "demo/public/css/jit.css"
	err = ioutil.WriteFile(outputPath, []byte(css), 0644)
	if err != nil {
		fmt.Printf("Error writing output file (%s): %v\n", outputPath, err)
		return
	}

	fmt.Printf("Success! Generated CSS written to %s\n", outputPath)
	fmt.Printf("Total CSS Size: %d bytes\n", len(css))
}

func GenerateUltimateJITCSS(html string) string {
	cssBuckets := make(map[string]*strings.Builder)
	keys := []string{"pocket", "mobile", "tablet", "laptop", "desktop", "cinema"}
	for _, k := range keys {
		cssBuckets[k] = &strings.Builder{}
	}
	generated := make(map[string]bool)

	mediaQueries := map[string]string{
		"cinema":  "@media (min-width: 1536px)",
		"desktop": "@media (min-width: 1280px)",
		"laptop":  "@media (max-width: 1200px)",
		"tablet":  "@media (max-width: 900px)",
		"mobile":  "@media (max-width: 600px)",
	}

	parseFullContext := func(fullClass string) (string, string, bool, bool, string) {
		screen, pseudo, isGroup, isNegative, remaining := "pocket", "", false, false, fullClass
		screens := []string{"cinema", "desktop", "laptop", "tablet", "mobile"}
		for _, s := range screens {
			if strings.HasPrefix(remaining, s+":") {
				screen = s
				remaining = strings.TrimPrefix(remaining, s+":")
				break
			}
		}
		if strings.HasPrefix(remaining, "group-hover:") {
			isGroup, remaining = true, strings.TrimPrefix(remaining, "group-hover:")
		} else {
			states := []string{"hover", "focus", "active"}
			for _, s := range states {
				if strings.HasPrefix(remaining, s+":") {
					pseudo, remaining = s, strings.TrimPrefix(remaining, s+":")
					break
				}
			}
		}
		if strings.HasPrefix(remaining, "-") {
			isNegative, remaining = true, strings.TrimPrefix(remaining, "-")
		}
		return screen, pseudo, isGroup, isNegative, remaining
	}

	type Pattern struct {
		Reg  string
		Type string
	}

	// TRẬT TỰ ƯU TIÊN: Cụ thể nhất -> Tổng quát nhất
	// Helper để xử lý giá trị có đơn vị
	parseValue := func(val string) string {
		if val == "auto" || val == "full" || val == "screen" || val == "min" || val == "max" {
			return val
		}
		// Hỗ trợ các đơn vị tường minh
		if strings.HasSuffix(val, "px") {
			return val // Giữ nguyên 60px
		}
		if strings.HasSuffix(val, "-percent") {
			return strings.TrimSuffix(val, "-percent") + "%" // 45-percent -> 45%
		}
		if strings.HasSuffix(val, "pct") {
			return strings.TrimSuffix(val, "pct") + "%" // 50pct -> 50%
		}
		if strings.HasSuffix(val, "%") {
			return val // 50% -> 50%
		}
		if strings.HasSuffix(val, "vh") || strings.HasSuffix(val, "vw") || strings.HasSuffix(val, "rem") || strings.HasSuffix(val, "em") {
			return val
		}
		// Mặc định là px nếu là số
		if _, err := strconv.Atoi(val); err == nil {
			return val + "px"
		}
		return val
	}

	// TRẬT TỰ ƯU TIÊN: Cụ thể nhất -> Tổng quát nhất
	orderedPatterns := []Pattern{
		// 1. Spacing & Borders (Hỗ trợ đơn vị: margin-60px, padding-5pct...)
		{`^(margin|padding|border)-(x|y)-([a-z0-9%.-]+)$`, "spacing-axis"},
		{`^(margin|padding|border)-(top|bottom|left|right)-([a-z0-9%.-]+)$`, "spacing-dir"},
		{`^(margin|padding)-([a-z0-9%.-]+)$`, "spacing-all"},
		{`^(border)-(\d+(?:px|rem|em|%)?)$`, "border-all"},

		// 2. Sizing
		{`^(width|height|max-width|min-width|max-height|min-height)-([a-z0-9%.-]+)$`, "sizing"},

		// 3. Typo (Kích thước chữ & Weight)
		{`^(font-size|text|font)-(\d+[a-z0-9%.-]*|bold|medium|light)$`, "typo-size"},
		{`^(line-height)-([0-9.]+)$`, "line-height"},
		{`^(letter-spacing)-(-?[\d.]+(px|em|rem)?)$`, "letter-spacing"},
		{`^(font-family)-([a-zA-Z0-9-]+)$`, "font-family"},
		{`^(font)-(mono|sans|serif)$`, "font-family"},
		{`^(text)-(center|left|right|justify|uppercase|lowercase|capitalize)$`, "text-style"},

		// ... (Giữ nguyên các pattern khác)
		{`^(flex)-(row|column|row-reverse|column-reverse)$`, "flex-direction"},
		{`^(justify)-(start|end|center|between|around|evenly)$`, "justify-content"},
		{`^(items)-(start|end|center|baseline|stretch)$`, "align-items"},
		{`^(gap)-([a-z0-9%.-]+)$`, "gap"},
		{`^(grid-columns)-(\d+)$`, "grid-columns"},
		{`^(display-)?(flex|grid|block|inline|inline-block|inline-flex|hidden)$`, "display"},
		{`^(position)-(relative|absolute|fixed|sticky)$`, "position"},
		{`^(z-index)-(\d+|sticky|overlay|above|below|behind)$`, "z-index"},

		// 5. Colors & Decor
		{`^(background|bg|color|text|border)-([a-zA-Z0-9]+)(?:-(\d+))?$`, "color"},
		{`^(rounded|opacity)-([a-z0-9%.-]+)$`, "misc-val"},
		{`^(shadow)-(small|medium|larger|giant)$`, "shadow"},
		{`^(shadow)-(brand|glow)$`, "shadow-color"},
		{`^(translate)-(x|y)-(-?[a-z0-9]+)$`, "transform-move"},
		{`^(transition)-(all|colors|opacity|transform)$`, "transition"},
		{`^(duration)-(\d+)$`, "duration"},
		{`^(rotate)-(-?\d+)$`, "rotate"},
		{`^(scale)-(\d+)$`, "scale"},
		{`^(backdrop-filter-blur)-(\d+)$`, "backdrop-filter"},
		{`^(object)-(contain|cover|fill|none|scale-down)$`, "object-fit"},
		{`^(translate)-(x|y)-(-?\d+)(-(percent))?$`, "translate"},
		{`^(cube-size)-(\d+)$`, "cube-size"},
		{`^(cube-duration)-(\d+)(s|ms)?$`, "cube-duration"},
		{`^container$`, "container"},
	}

	reClass := regexp.MustCompile(`class="([^"]+)"`)
	matchesHTML := reClass.FindAllStringSubmatch(html, -1)
	allClasses := []string{}
	for _, m := range matchesHTML {
		allClasses = append(allClasses, strings.Split(m[1], " ")...)
	}

	for _, className := range allClasses {
		className = strings.TrimSpace(className)
		if className == "" || generated[className] {
			continue
		}
		screen, pseudo, isGroup, isNegative, coreClass := parseFullContext(className)
		cssContent := ""

		for _, pat := range orderedPatterns {
			re := regexp.MustCompile(pat.Reg)
			match := re.FindStringSubmatch(coreClass)
			if len(match) > 0 {
				switch pat.Type {
				case "container":
					cssContent = "width: 100%; margin-left: auto; margin-right: auto; max-width: 1280px; padding-left: 1.5rem; padding-right: 1.5rem;"
				case "display":
					val := match[2]
					if val == "hidden" {
						cssContent = "display: none;"
					} else {
						cssContent = fmt.Sprintf("display: %s;", val)
					}
				case "position":
					cssContent = fmt.Sprintf("position: %s;", match[2])
				case "z-index":
					val, zMap := match[2], map[string]string{"sticky": "100", "above": "10", "below": "-1", "behind": "-10", "overlay": "1000"}
					if m, ok := zMap[val]; ok {
						val = m
					}
					if isNegative {
						val = "-" + val
					}
					cssContent = fmt.Sprintf("z-index: %s;", val)
				case "flex-direction":
					cssContent = fmt.Sprintf("flex-direction: %s;", match[2])
				case "justify-content":
					v := match[2]
					if v == "start" || v == "end" {
						v = "flex-" + v
					}
					if v == "between" || v == "around" || v == "evenly" {
						v = "space-" + v
					}
					cssContent = fmt.Sprintf("justify-content: %s;", v)
				case "align-items":
					v := match[2]
					if v == "start" || v == "end" {
						v = "flex-" + v
					}
					cssContent = fmt.Sprintf("align-items: %s;", v)
				case "grid-columns":
					cssContent = fmt.Sprintf("grid-template-columns: repeat(%s, minmax(0, 1fr));", match[2])

				case "gap":
					cssContent = fmt.Sprintf("gap: %s;", parseValue(match[2]))
				case "sizing":
					prop, val := match[1], parseValue(match[2])
					if val == "full" {
						val = "100%"
					}
					if val == "screen" {
						if strings.Contains(prop, "width") {
							val = "100vw"
						} else {
							val = "100vh"
						}
					}
					cssContent = fmt.Sprintf("%s: %s;", prop, val)
				case "spacing-axis":
					prop, axis, val := match[1], match[2], parseValue(match[3])
					if isNegative && prop == "margin" {
						val = "-" + val
					}
					if prop == "border" {
						if axis == "x" {
							cssContent = fmt.Sprintf("border-left-width: %s; border-right-width: %s; border-left-style: solid; border-right-style: solid;", val, val)
						} else {
							cssContent = fmt.Sprintf("border-top-width: %s; border-bottom-width: %s; border-top-style: solid; border-bottom-style: solid;", val, val)
						}
					} else {
						if axis == "x" {
							cssContent = fmt.Sprintf("%s-left: %s; %s-right: %s;", prop, val, prop, val)
						} else {
							cssContent = fmt.Sprintf("%s-top: %s; %s-bottom: %s;", prop, val, prop, val)
						}
					}
				case "spacing-dir":
					prop, dir, val := match[1], match[2], parseValue(match[3])
					if isNegative && prop == "margin" {
						val = "-" + val
					}
					if prop == "border" {
						cssContent = fmt.Sprintf("border-%s-width: %s; border-%s-style: solid;", dir, val, dir)
					} else {
						cssContent = fmt.Sprintf("%s-%s: %s;", prop, dir, val)
					}
				case "spacing-all":
					prop, val := match[1], parseValue(match[2])
					if isNegative && prop == "margin" {
						val = "-" + val
					}
					cssContent = fmt.Sprintf("%s: %s;", prop, val)

				case "color":
					// Regex: ^(background|color|text|border)-([a-zA-Z0-9]+)(?:[-/](\d+))?$
					pStr, pre, cName, op := "color", match[1], match[2], match[3]
					// cName có thể là "white" hoặc "gray-400" (nếu sau này có scale)
					// Logic mới: Nếu cName kết thúc bằng số, mà không có op riêng, thì đó là tên màu.
					// Nếu có op riêng (do match[4] bắt được), thì đó là opacity.

					if pre == "background" || pre == "bg" {
						pStr = "background-color"
					}
					if pre == "border" {
						pStr = "border-color"
					}
					if cName == "transparent" {
						cssContent = fmt.Sprintf("%s: transparent;", pStr)
					} else if cName == "current" {
						cssContent = fmt.Sprintf("%s: currentColor;", pStr)
					} else {
						vV := fmt.Sprintf("var(--color-%s-rgb)", cName)
						if op != "" {
							var f float64
							fmt.Sscanf(op, "%f", &f)
							if f > 1 {
								f /= 100.0
							}
							cssContent = fmt.Sprintf("%s: rgba(%s, %.2g);", pStr, vV, f)
						} else {
							cssContent = fmt.Sprintf("%s: rgb(%s);", pStr, vV)
						}
					}
				case "text-style":
					if match[2] == "center" || match[2] == "left" || match[2] == "right" || match[2] == "justify" {
						cssContent = fmt.Sprintf("text-align: %s;", match[2])
					} else {
						cssContent = fmt.Sprintf("text-transform: %s;", match[2])
					}
				case "font-family":
					if match[2] == "mono" {
						cssContent = "font-family: 'JetBrains Mono', monospace;"
					} else if match[2] == "sans" {
						cssContent = "font-family: 'Outfit', sans-serif;"
					} else {
						cssContent = fmt.Sprintf("font-family: '%s', sans-serif;", match[2])
					}
				case "line-height":
					cssContent = fmt.Sprintf("line-height: %s;", match[2])
				case "shadow":
					sM := map[string]string{"small": "0 1px 2px 0 rgba(0,0,0,0.05)", "medium": "0 4px 6px -1px rgba(0,0,0,0.1)", "larger": "0 10px 15px -3px rgba(0,0,0,0.1)", "giant": "0 20px 25px -5px rgba(0,0,0,0.1), 0 10px 10px -5px rgba(0,0,0,0.04)"}
					cssContent = fmt.Sprintf("box-shadow: %s;", sM[match[2]])
				case "shadow-color":
					if match[2] == "brand" {
						cssContent = "box-shadow: 0 4px 14px 0 rgba(var(--color-brand-rgb), 0.39);"
					} else if match[2] == "glow" {
						cssContent = "box-shadow: 0 0 20px rgba(var(--color-brand-rgb), 0.5);"
					}
				case "transform-move":
					axis, val := strings.ToUpper(match[2]), parseValue(match[3])
					// Fix: handle negative values explicitly if regex allows '-2px' but parseValue might need hints?
					// parseValue handles it if passed correctly. regex is -?[a-z0-9]+
					// If val is '2px', axis='Y' -> translateY(2px)
					// If val is '-3px', -> translateY(-3px)
					cssContent = fmt.Sprintf("transform: translate%s(%s);", axis, val)
				case "border-width":
					d, v := match[2], match[3]
					cssContent = fmt.Sprintf("border-%s-width: %spx; border-%s-style: solid;", d, v, d)
				case "border-all":
					cssContent = fmt.Sprintf("border-width: %s; border-style: solid;", parseValue(match[2]))

				// ... (Các case khác cập nhật tương tự với parseValue)
				case "typo-size":
					pre, val := match[1], match[2]
					isW := (val == "bold" || val == "medium" || val == "light" || (strings.HasPrefix(pre, "font") && len(val) == 3 && strings.HasSuffix(val, "00")))
					if isW {
						cssContent = fmt.Sprintf("font-weight: %s;", val)
					} else {
						cssContent = fmt.Sprintf("font-size: %s;", parseValue(val))
					}
				case "letter-spacing":
					cssContent = fmt.Sprintf("letter-spacing: %s;", parseValue(match[2]))

				case "misc-val":
					pre, rawVal := match[1], match[2]
					if pre == "rounded" {
						val := parseValue(rawVal)
						if rawVal == "full" {
							val = "9999px"
						}
						cssContent = fmt.Sprintf("border-radius: %s;", val)
					} else {
						// Opacity logic
						v := rawVal
						if v == "100" {
							v = "1"
						} else if !strings.Contains(v, ".") {
							v = "0." + v
						}
						cssContent = fmt.Sprintf("opacity: %s;", v)
					}
				case "transition":
					val := match[2]
					if val == "all" {
						cssContent = "transition-property: all; transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1); transition-duration: 150ms;"
					} else if val == "colors" {
						cssContent = "transition-property: color, background-color, border-color, text-decoration-color, fill, stroke; transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1); transition-duration: 150ms;"
					} else if val == "opacity" {
						cssContent = "transition-property: opacity; transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1); transition-duration: 150ms;"
					} else if val == "transform" {
						cssContent = "transition-property: transform; transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1); transition-duration: 150ms;"
					}
				case "duration":
					cssContent = fmt.Sprintf("transition-duration: %sms;", match[2])
				case "rotate":
					cssContent = fmt.Sprintf("transform: rotate(%sdeg);", match[2])
				case "scale":
					v := match[2]
					if len(v) > 0 && !strings.Contains(v, ".") && v != "100" {
						v = "0." + v // scale-95 -> 0.95
					} else if v == "100" {
						v = "1"
					}
					cssContent = fmt.Sprintf("transform: scale(%s);", v)
				case "backdrop-filter":
					cssContent = fmt.Sprintf("backdrop-filter: blur(%spx); -webkit-backdrop-filter: blur(%spx);", match[2], match[2])
				case "object-fit":
					cssContent = fmt.Sprintf("object-fit: %s;", match[2])
				case "translate":
					axis, val := match[2], parseValue(match[3])
					if strings.HasSuffix(val, "-percent") { // Fix regex capture layout if needed, but parseValue handles it
						val = strings.TrimSuffix(val, "-percent") + "%"
					}
					// match[4] might be "-percent" if I used that regex, let's check parseValue usage
					cssContent = fmt.Sprintf("transform: translate%s(%s);", strings.ToUpper(axis), val)
				case "cube-size":
					size, _ := strconv.Atoi(match[2])
					cssContent = fmt.Sprintf("--cube-size: %dpx; --cube-face-translate-z: %dpx;", size, size/2)
				case "cube-duration":
					unit := match[3]
					if unit == "" {
						unit = "s"
					}
					cssContent = fmt.Sprintf("--cube-duration: %s%s;", match[2], unit)
				}
				break
			}
		}

		if cssContent != "" {
			esc := strings.ReplaceAll(className, ":", "\\:")
			esc = strings.ReplaceAll(esc, ".", "\\.")
			esc = strings.ReplaceAll(esc, "/", "\\/")
			if strings.HasPrefix(esc, "-") {
				esc = "\\" + esc
			}
			sel := "." + esc
			if isGroup {
				sel = ".group:hover ." + esc
			} else if pseudo != "" {
				sel += ":" + pseudo
			}
			cssBuckets[screen].WriteString(fmt.Sprintf("\t%s { %s }\n", sel, cssContent))
			generated[className] = true
		}
	}

	var final strings.Builder
	final.WriteString("/* Kitwork Industrial Engine JIT */\n")
	final.WriteString(cssBuckets["pocket"].String())
	for _, s := range []string{"mobile", "tablet", "laptop", "desktop", "cinema"} {
		if content := cssBuckets[s].String(); content != "" {
			final.WriteString(fmt.Sprintf("\n/* %s */\n%s {\n%s}\n", s, mediaQueries[s], content))
		}
	}
	return final.String()
}
