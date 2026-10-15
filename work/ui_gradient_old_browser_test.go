package work

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestBrowserGradientTextWithoutColorMix looks at the pixels of gradient text whose stops are color-mix()
// (ink-10) in a browser that clips a background to the text but cannot read color-mix(): Chrome
// and Edge 99–110 and Safari 15.4–16.1, the floor KITWORK_UI §11 promises. Such a browser drops every
// declaration that holds color-mix() when it parses it; the test makes today's Edge one by serving the
// stylesheets with color-mix( renamed to a function no browser has, which drops exactly those
// declarations (review of 12/10/2026). The letters must still be painted — the gradient's rule holds only
// where the browser also reads color-mix() (@supports … and (color:color-mix(…))), so it keeps a solid
// colour — never a transparent fill over no image, which painted 0 letter pixels. CONTROL: served as
// written, the same text is the gradient.
func TestBrowserGradientTextWithoutColorMix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the gradient screenshot in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, map[string]string{
		"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.gradients({ ember: { type: "linear", direction: "to-end", stops: [ { color: "ink-10", at: 0 }, { color: "brand+15", at: 100 } ] } });
`,
		"localhost/page.ui":    `<page><content /></page>`,
		"localhost/content.ui": `<content padding="4"><text heading="1" size="huge" color="ember">MMMMMMMM</text></content>`,
	})
	older := false // serve the stylesheets as a browser without color-mix() reads them
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		forward.Header.Del("Accept-Encoding") // the stylesheets are rewritten as text
		answer := httptest.NewRecorder()
		serving.Lock()
		tenant.Serve(answer, forward)
		renamed := older
		serving.Unlock()
		for name, values := range answer.Header() {
			response.Header()[name] = values
		}
		body := answer.Body.String()
		if renamed && strings.HasSuffix(request.URL.Path, ".css") {
			body = strings.ReplaceAll(body, "color-mix(", "colour-mix(")
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	// letters are the pixels that are not the page's background (the top-left corner): how many, and the
	// widest spread between two channels of one of them
	letters := func(name string) (count, spread int) {
		shot := screenshotOf(t, browser, server.URL+"/", name, "")
		bounds := shot.Bounds()
		background := shot.At(bounds.Min.X+1, bounds.Min.Y+1)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				pixel := shot.At(x, y)
				if pixel == background {
					continue
				}
				r, g, b, _ := pixel.RGBA()
				r, g, b = r>>8, g>>8, b>>8
				count++
				spread = max(spread, int(max(r, g, b)-min(r, g, b)))
			}
		}
		return count, spread
	}
	if count, spread := letters("as written"); count < 500 || spread < 30 {
		t.Fatalf("control, as written: %d letter pixels, their channels at most %d apart: want the gradient's colours", count, spread)
	}
	serving.Lock()
	older = true
	serving.Unlock()
	if count, _ := letters("without color-mix"); count < 500 {
		t.Errorf("without color-mix(): %d letter pixels: want the text painted in a solid colour, not a transparent fill over no image", count)
	}
}
