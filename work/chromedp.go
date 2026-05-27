package work

import (
	"context"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/kitwork/engine/value"
)

var (
	globalChromeCtx    context.Context
	globalChromeCancel context.CancelFunc
	globalChromeOnce   sync.Once
	globalChromeMu     sync.Mutex
)

func initGlobalChrome() {
	globalChromeOnce.Do(func() {
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
		)
		allocCtx, _ := chromedp.NewExecAllocator(context.Background(), opts...)
		globalChromeCtx, globalChromeCancel = chromedp.NewContext(allocCtx)
		// Run a dummy task to force start Chrome process
		_ = chromedp.Run(globalChromeCtx)
	})
}

func (w *KitWork) Chromedp() *Chromedp {
	return &Chromedp{tenant: w.tenant}
}

func (w *KitWork) Screenshot() *Chromedp {
	return w.Chromedp()
}

type Chromedp struct {
	tenant *Tenant
}

// Fluent Chaining API: page(url).viewport(w,h).wait(ms).capture()
func (c *Chromedp) Page(urlVal value.Value) *ChromeOptions {
	initGlobalChrome()
	return &ChromeOptions{
		tenant: c.tenant,
		url:    urlVal.Text(),
		width:  1280,
		height: 720,
		delay:  1 * time.Second,
	}
}

func (c *Chromedp) Navigate(urlVal value.Value) *ChromeOptions {
	return c.Page(urlVal)
}

type ChromeOptions struct {
	tenant *Tenant
	url    string
	width  int
	height int
	delay  time.Duration
	err    error
}

func (co *ChromeOptions) Viewport(widthVal value.Value, heightVal value.Value) *ChromeOptions {
	if co.err != nil {
		return co
	}
	if widthVal.K == value.Number {
		co.width = int(widthVal.N)
	}
	if heightVal.K == value.Number {
		co.height = int(heightVal.N)
	}
	return co
}

func (co *ChromeOptions) Wait(delayVal value.Value) *ChromeOptions {
	if co.err != nil {
		return co
	}
	if delayVal.K == value.Number {
		co.delay = time.Duration(delayVal.N) * time.Millisecond
	}
	return co
}
