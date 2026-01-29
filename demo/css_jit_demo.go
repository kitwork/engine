package main

import (
	"fmt"
	"io/ioutil"
	"regexp"
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
	orderedPatterns := []Pattern{
		// 1. Spacing & Borders (Số liệu - Ưu tiên cao nhất để không bị nhầm vào Color)
		{`^(margin|padding|border)-(x|y)-(\d+|auto)$`, "spacing-axis"},
		{`^(margin|padding|border)-(top|bottom|left|right)-(\d+|auto)$`, "spacing-dir"},
		{`^(margin|padding)-(\d+|auto)$`, "spacing-all"},
		{`^(border)-(\d+)$`, "border-all"},

		// 2. Sizing
		{`^(width|height|max-width|min-width|max-height|min-height)-(\d+|full|screen|auto|min|max)(-(percent))?$`, "sizing"},

		// 3. Typo (Kích thước chữ & Weight)
		{`^(font-size|text|font)-(\d+|bold|medium|light)$`, "typo-size"},
		{`^(line-height)-([0-9.]+)$`, "line-height"},
		{`^(letter-spacing)-(-?[\d.]+)$`, "letter-spacing"},
		{`^(font-family)-([a-z0-9-]+)$`, "font-family"},
		{`^(text)-(center|left|right|justify|uppercase|lowercase|capitalize)$`, "text-style"},

		// 4. Layout & Flex/Grid
		{`^(flex)-(row|column|row-reverse|column-reverse)$`, "flex-direction"},
		{`^(justify)-(start|end|center|between|around|evenly)$`, "justify-content"},
		{`^(items)-(start|end|center|baseline|stretch)$`, "align-items"},
		{`^(gap)-(\d+)$`, "gap"},
		{`^(grid-columns)-(\d+)$`, "grid-cols"},
		{`^(display-)?(flex|grid|block|inline|inline-block|inline-flex|hidden)$`, "display"},
		{`^(position)-(relative|absolute|fixed|sticky)$`, "position"},
		{`^(z-index)-(\d+|sticky|overlay|above|below|behind)$`, "z-index"},

		// 5. Colors & Decor (Ưu tiên thấp nhất vì Regex của nó bao quát chuỗi [a-z0-9-])
		{`^(background|color|text|border)-([a-zA-Z0-9-]+)(/(\d+))?$`, "color"},
		{`^(rounded|opacity)-(\d+|full)$`, "misc-val"},
		{`^(shadow)-(small|medium|larger|giant)$`, "shadow"},
		{`^(transition)-(all|colors|opacity|transform)$`, "transition"},
		{`^(duration)-(\d+)$`, "duration"},
		{`^(rotate)-(-?\d+)$`, "rotate"},
		{`^(scale)-(\d+)$`, "scale"},
		{`^(translate)-(x|y)-(-?\d+)(-(percent))?$`, "translate"},
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
				case "gap":
					cssContent = fmt.Sprintf("gap: %spx;", match[2])
				case "grid-cols":
					cssContent = fmt.Sprintf("grid-template-columns: repeat(%s, minmax(0, 1fr));", match[2])
				case "sizing":
					prop, val, unit := match[1], match[2], "px"
					if len(match) > 4 && match[4] == "percent" {
						unit = "%"
					}
					if val == "full" {
						val, unit = "100%", ""
					}
					if val == "screen" {
						if strings.Contains(prop, "width") {
							val = "100vw"
						} else {
							val = "100vh"
						}
						unit = ""
					}
					if val == "auto" || val == "min" || val == "max" {
						unit = ""
					}
					cssContent = fmt.Sprintf("%s: %s%s;", prop, val, unit)
				case "spacing-axis":
					prop, axis, val := match[1], match[2], match[3]
					if val != "auto" {
						if isNegative && prop == "margin" {
							val = "-" + val
						}
						val += "px"
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
					prop, val := match[1], match[3]
					dir := match[2]
					if val != "auto" {
						if isNegative && prop == "margin" {
							val = "-" + val
						}
						val += "px"
					}
					if prop == "border" {
						cssContent = fmt.Sprintf("border-%s-width: %s; border-%s-style: solid;", dir, val, dir)
					} else {
						cssContent = fmt.Sprintf("%s-%s: %s;", prop, dir, val)
					}
				case "spacing-all":
					prop, val := match[1], match[2]
					if val != "auto" {
						if isNegative && prop == "margin" {
							val = "-" + val
						}
						val += "px"
					}
					cssContent = fmt.Sprintf("%s: %s;", prop, val)
				case "border-width":
					d, v := match[2], match[3]
					cssContent = fmt.Sprintf("border-%s-width: %spx; border-%s-style: solid;", d, v, d)
				case "border-all":
					cssContent = fmt.Sprintf("border-width: %spx; border-style: solid;", match[2])
				case "font-family":
					cssContent = fmt.Sprintf("font-family: '%s', sans-serif;", match[2])
				case "line-height":
					cssContent = fmt.Sprintf("line-height: %s;", match[2])
				case "letter-spacing":
					cssContent = fmt.Sprintf("letter-spacing: %spx;", match[2])
				case "text-style":
					if match[2] == "center" || match[2] == "left" || match[2] == "right" || match[2] == "justify" {
						cssContent = fmt.Sprintf("text-align: %s;", match[2])
					} else {
						cssContent = fmt.Sprintf("text-transform: %s;", match[2])
					}
				case "typo-size":
					pre, val, isW := match[1], match[2], false
					if val == "bold" || val == "medium" || val == "light" {
						isW = true
					}
					if pre == "font" && len(val) == 3 && strings.HasSuffix(val, "00") {
						isW = true
					}
					if isW {
						cssContent = fmt.Sprintf("font-weight: %s;", val)
					} else {
						cssContent = fmt.Sprintf("font-size: %spx;", val)
					}
				case "color":
					pStr, pre, cName, op := "color", match[1], match[2], match[4]
					if pre == "background" {
						pStr = "background-color"
					}
					if pre == "border" {
						pStr = "border-color"
					}
					if pre == "background" && cName == "color" {
						cName = "bg-color"
					}
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
				case "shadow":
					sM := map[string]string{"small": "0 1px 2px 0 rgba(0,0,0,0.05)", "medium": "0 4px 6px -1px rgba(0,0,0,0.1)", "larger": "0 10px 15px -3px rgba(0,0,0,0.1)", "giant": "0 20px 25px -5px rgba(0,0,0,0.1), 0 10px 10px -5px rgba(0,0,0,0.04)"}
					cssContent = fmt.Sprintf("box-shadow: %s;", sM[match[2]])
				case "misc-val":
					pre, v := match[1], match[2]
					if pre == "rounded" {
						if v == "full" {
							v = "9999px"
						} else {
							v += "px"
						}
						cssContent = fmt.Sprintf("border-radius: %s;", v)
					} else {
						if v == "100" {
							v = "1"
						} else {
							v = "0." + v
						}
						cssContent = fmt.Sprintf("opacity: %s;", v)
					}
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
