package qrcode

import (
	"os"
	"strings"
	"testing"

	goqrcode "github.com/skip2/go-qrcode"
)

func TestSvgRendering(t *testing.T) {
	opts := &Options{
		Data:     "https://kitwork.com",
		Template: "circle",
		Padding:  2,
		Level:    goqrcode.Medium,
		Size:     256,
		Cells: Cells{
			Active: Cell{
				Color:   "#005ba1",
				Size:    0.85,
				Rounded: 0,
				Opacity: 1.0,
			},
			Passive: Cell{
				Color:   "#cccccc",
				Size:    0.5,
				Rounded: 0,
				Opacity: 0.3,
			},
		},
		Finders: Finders{
			TopLeft: Finder{
				Color:   "#005ba1",
				Stroke:  "#ff5f56",
				Rounded: 3.5,
			},
			TopRight: Finder{
				Color:   "#005ba1",
				Stroke:  "#ff5f56",
				Rounded: 3.5,
			},
			BottomLeft: Finder{
				Color:   "#005ba1",
				Stroke:  "#ff5f56",
				Rounded: 3.5,
			},
		},
		Background: Background{
			Color:   "#ffffff",
			Rounded: 5.0,
		},
	}

	svg, err := opts.Svg()
	if err != nil {
		t.Fatalf("failed to generate SVG: %v", err)
	}

	if !strings.Contains(svg, "<svg") || !strings.Contains(svg, "</svg>") {
		t.Errorf("rendered string is not a valid SVG: %s", svg)
	}

	// Verify that paths are drawn (since template is circle, represented as a merged path)
	if !strings.Contains(svg, "<path") {
		t.Errorf("rendered SVG should contain path tags: %s", svg)
	}

	// Verify that the custom color is present
	if !strings.Contains(svg, `fill="#005ba1"`) {
		t.Errorf("rendered SVG should use active cell color: %s", svg)
	}
}

func TestPngRendering(t *testing.T) {
	opts := &Options{
		Data:     "https://kitwork.com",
		Template: "square",
		Padding:  2,
		Level:    goqrcode.Medium,
		Size:     128,
		Cells: Cells{
			Active: Cell{
				Color:   "#000000",
				Size:    1.0,
				Rounded: 0,
				Opacity: 1.0,
			},
		},
		Finders: Finders{
			TopLeft: Finder{
				Color:   "#000000",
				Stroke:  "#000000",
				Rounded: 0,
			},
			TopRight: Finder{
				Color:   "#000000",
				Stroke:  "#000000",
				Rounded: 0,
			},
			BottomLeft: Finder{
				Color:   "#000000",
				Stroke:  "#000000",
				Rounded: 0,
			},
		},
		Background: Background{
			Color: "#ffffff",
		},
	}

	pngBytes, err := opts.Png()
	if err != nil {
		t.Fatalf("failed to generate PNG: %v", err)
	}

	if len(pngBytes) < 100 {
		t.Errorf("rendered PNG is too small: %d bytes", len(pngBytes))
	}

	// Verify PNG signature (first 8 bytes: 89 50 4E 47 0D 0A 1A 0A)
	if len(pngBytes) >= 4 && string(pngBytes[1:4]) != "PNG" {
		t.Errorf("rendered bytes do not have a valid PNG signature")
	}
}

func TestGenerateSampleQrcodes(t *testing.T) {
	// Sample 1: Circular template, Shopee logo with stroke, orange color, custom finders, dashed circular outer frame
	shopeeOpts := &Options{
		Data:     "https://shopee.vn",
		Template: "circle",
		Padding:  4, // increase padding slightly to contain background border
		Level:    goqrcode.High,
		Size:     400,
		Cells: Cells{
			Active: Cell{
				Color:   "auto", // will extract Shopee's brand orange color
				Size:    0.80,
				Opacity: 1.0,
			},
			Passive: Cell{
				Color:   "#f2f2f2",
				Size:    0.45,
				Opacity: 0.25,
			},
		},
		Finders: Finders{
			TopLeft: Finder{
				Color:   "auto",
				Stroke:  "auto",
				Rounded: 3.5, // Circular
			},
			TopRight: Finder{
				Color:   "auto",
				Stroke:  "auto",
				Rounded: 3.5,
			},
			BottomLeft: Finder{
				Color:   "auto",
				Stroke:  "auto",
				Rounded: 3.5,
			},
		},
		Center: Center{
			Image:      "D:/project/resource/assets/images/logo/shopee.png",
			Background: "#ffffff",
			Stroke:     "auto", // matches extracted orange
			Shape:      "circle",
			Size:       0.22,
			Padding:    0.2,
		},
		Background: Background{
			Color:  "#ffffff",
			Stroke: "auto", // matches extracted orange
			Dashed: "circular",
			Border: 0.6,
		},
	}

	shopeeBytes, err := shopeeOpts.Png()
	if err == nil {
		_ = os.WriteFile("C:/Users/huynh/.gemini/antigravity-ide/brain/6626b499-fad7-442d-91c5-c702b171e1e7/sample_shopee.png", shopeeBytes, 0644)
	} else {
		t.Errorf("failed to render shopee: %v", err)
	}

	// Sample 2: Diamond template, Kitnext logo, linear cell gradients, custom alignment styling, stroke on logo
	kitnextOpts := &Options{
		Data:     "https://kitnext.com",
		Template: "diamond",
		Padding:  3,
		Level:    goqrcode.High,
		Size:     400,
		Cells: Cells{
			Active: Cell{
				Size:    0.85,
				Opacity: 1.0,
				Gradient: &Gradient{
					Type:   "linear",
					Colors: []string{"#0f172a", "#0284c7"}, // slate to sky blue gradient
					Angle:  45.0,
				},
			},
			Alignment: Cell{
				Color:   "#ff5f56",
				Rounded: 1.0,
			},
		},
		Finders: Finders{
			TopLeft: Finder{
				Color:   "auto",
				Stroke:  "#1e293b",
				Rounded: 1.5,
			},
			TopRight: Finder{
				Color:   "auto",
				Stroke:  "#1e293b",
				Rounded: 1.5,
			},
			BottomLeft: Finder{
				Color:   "auto",
				Stroke:  "#1e293b",
				Rounded: 1.5,
			},
		},
		Center: Center{
			Image:      "D:/project/resource/assets/images/logo/kitnext.png",
			Background: "#f8fafc",
			Stroke:     "#38bdf8", // light blue stroke around logo container
			Shape:      "square",
			Size:       0.20,
			Padding:    0.15,
		},
		Background: Background{
			Color: "#ffffff",
		},
	}

	kitnextBytes, err := kitnextOpts.Png()
	if err == nil {
		_ = os.WriteFile("C:/Users/huynh/.gemini/antigravity-ide/brain/6626b499-fad7-442d-91c5-c702b171e1e7/sample_kitnext.png", kitnextBytes, 0644)
	} else {
		t.Errorf("failed to render kitnext: %v", err)
	}

	// Sample 3: Heart template, Wedding Ring logo, pink radial cell gradient, stroke on logo
	weddingOpts := &Options{
		Data:     "https://wedding.kitwork.com",
		Template: "heart",
		Padding:  3,
		Level:    goqrcode.High,
		Size:     400,
		Cells: Cells{
			Active: Cell{
				Size:    0.85,
				Opacity: 1.0,
				Gradient: &Gradient{
					Type:   "radial",
					Colors: []string{"#f43f5e", "#9f1239"}, // rose to deep red gradient
				},
			},
		},
		Finders: Finders{
			TopLeft: Finder{
				Color:   "#f43f5e",
				Stroke:  "#9f1239",
				Rounded: 3.5,
			},
			TopRight: Finder{
				Color:   "#f43f5e",
				Stroke:  "#9f1239",
				Rounded: 3.5,
			},
			BottomLeft: Finder{
				Color:   "#f43f5e",
				Stroke:  "#9f1239",
				Rounded: 3.5,
			},
		},
		Center: Center{
			Image:      "D:/project/resource/assets/images/logo/wedding-ring.png",
			Background: "#fff1f2",
			Stroke:     "#fda4af",
			Shape:      "circle",
			Size:       0.24,
			Padding:    0.2,
		},
		Background: Background{
			Color: "#fff1f2",
		},
	}

	weddingBytes, err := weddingOpts.Png()
	if err == nil {
		_ = os.WriteFile("C:/Users/huynh/.gemini/antigravity-ide/brain/6626b499-fad7-442d-91c5-c702b171e1e7/sample_wedding.png", weddingBytes, 0644)
	} else {
		t.Errorf("failed to render wedding: %v", err)
	}
}
