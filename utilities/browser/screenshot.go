package browser

import (
	"context"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/kitwork/engine/value"
)

func (c *Chromedp) Capture(urlVal value.Value, options value.Value) value.Value {
	initGlobalChrome()

	urlStr := urlVal.Text()
	width := 1280
	height := 720
	delay := 1 * time.Second
	timeout := 15 * time.Second

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
		if toOpt, ok := opts["timeout"]; ok {
			timeout = parseScreenshotTimeout(toOpt, timeout)
		}
	}

	return runScreenshotCapture(urlStr, width, height, delay, timeout)
}

func parseScreenshotTimeout(v value.Value, def time.Duration) time.Duration {
	if v.K == value.Number {
		if v.N > 0 {
			return time.Duration(v.N) * time.Second
		}
		return def
	}
	if v.K == value.String {
		if d, err := time.ParseDuration(v.Text()); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func (c *Chromedp) Screenshot(urlVal value.Value, options value.Value) value.Value {
	return c.Capture(urlVal, options)
}

func (co *ChromeOptions) Capture() value.Value {
	if co.err != nil {
		return value.Value{K: value.Invalid, V: co.err.Error()}
	}
	return runScreenshotCapture(co.url, co.width, co.height, co.delay, co.timeout)
}

func (co *ChromeOptions) Screenshot() value.Value {
	return co.Capture()
}

func runScreenshotCapture(urlStr string, width, height int, delay, timeout time.Duration) value.Value {
	globalChromeMu.Lock()
	defer globalChromeMu.Unlock()

	if globalChromeCtx == nil {
		return value.Value{K: value.Invalid, V: "chrome not initialized"}
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	ctx, cancel := context.WithTimeout(globalChromeCtx, timeout)
	defer cancel()

	tabCtx, tabCancel := chromedp.NewContext(ctx)
	defer tabCancel()

	var buf []byte
	actions := []chromedp.Action{
		chromedp.EmulateViewport(int64(width), int64(height)),
		chromedp.Navigate(urlStr),
	}
	if delay > 0 {
		actions = append(actions, chromedp.Sleep(delay))
	}
	actions = append(actions, chromedp.CaptureScreenshot(&buf))

	if err := chromedp.Run(tabCtx, actions...); err != nil {
		return screenshotErr(err)
	}
	if len(buf) == 0 {
		return value.Value{K: value.Invalid, V: "empty screenshot (page did not render in time)"}
	}
	return value.New(buf)
}

func screenshotErr(err error) value.Value {
	if strings.Contains(err.Error(), "exec:") {
		return value.Value{K: value.Invalid, V: "exec: Google Chrome not found"}
	}
	return value.Value{K: value.Invalid, V: err.Error()}
}
