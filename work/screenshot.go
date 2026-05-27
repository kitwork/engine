package work

import (
	"context"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/kitwork/engine/value"
)

// 1. Direct Capture API: screenshot.capture(url, options) or chromedp.capture(url, options)
func (c *Chromedp) Capture(urlVal value.Value, options value.Value) value.Value {
	initGlobalChrome()

	urlStr := urlVal.Text()
	width := 1280
	height := 720
	delay := 1 * time.Second

	if options.K == value.Map {
		opts := options.Map()
		if wOpt, ok := opts["width"]; ok && wOpt.K == value.Number {
			width = int(wOpt.N)
		}
		if hOpt, ok := opts["height"]; ok && hOpt.K == value.Number {
			height = int(hOpt.N)
		}
		if tOpt, ok := opts["wait"]; ok && tOpt.K == value.Number {
			delay = time.Duration(tOpt.N) * time.Millisecond
		}
	}

	return runScreenshotCapture(urlStr, width, height, delay)
}

func (c *Chromedp) Screenshot(urlVal value.Value, options value.Value) value.Value {
	return c.Capture(urlVal, options)
}

// 2. Chaining Capture API: page(url).viewport(w,h).wait(ms).capture()
func (co *ChromeOptions) Capture() value.Value {
	if co.err != nil {
		return value.Value{K: value.Invalid, V: co.err.Error()}
	}
	return runScreenshotCapture(co.url, co.width, co.height, co.delay)
}

func (co *ChromeOptions) Screenshot() value.Value {
	return co.Capture()
}

func runScreenshotCapture(urlStr string, width, height int, delay time.Duration) value.Value {
	globalChromeMu.Lock()
	defer globalChromeMu.Unlock()

	if globalChromeCtx == nil {
		return value.Value{K: value.Invalid, V: "chrome not initialized"}
	}

	// Create tab context with 30s timeout
	ctx, cancel := context.WithTimeout(globalChromeCtx, 30*time.Second)
	defer cancel()

	tabCtx, tabCancel := chromedp.NewContext(ctx)
	defer tabCancel()

	var buf []byte
	var actions []chromedp.Action

	actions = append(actions, chromedp.EmulateViewport(int64(width), int64(height)))
	actions = append(actions, chromedp.Navigate(urlStr))
	if delay > 0 {
		actions = append(actions, chromedp.Sleep(delay))
	}
	actions = append(actions, chromedp.CaptureScreenshot(&buf))

	err := chromedp.Run(tabCtx, actions...)
	if err != nil {
		if strings.Contains(err.Error(), "exec:") {
			return value.Value{K: value.Invalid, V: "exec: Google Chrome not found"}
		}
		return value.Value{K: value.Invalid, V: err.Error()}
	}

	return value.New(buf)
}
