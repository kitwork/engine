package work

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/kitwork/engine/value"
)

func (w *KitWork) Browser() *Browser {
	return &Browser{
		Chromedp: Chromedp{tenant: w.tenant},
	}
}

type Browser struct {
	Chromedp
	ctx    context.Context
	cancel context.CancelFunc
	width  int
	height int
	err    error
}

func (b *Browser) New(opts ...value.Value) *Browser {
	initGlobalChrome()
	globalChromeMu.Lock()
	defer globalChromeMu.Unlock()

	if b.cancel != nil {
		b.cancel()
	}

	ctx, cancel := chromedp.NewContext(globalChromeCtx)
	nb := &Browser{
		Chromedp: Chromedp{tenant: b.tenant},
		ctx:      ctx,
		cancel:   cancel,
		width:    1280,
		height:   720,
	}

	if len(opts) > 0 && opts[0].K == value.Map {
		m := opts[0].Map()
		if wOpt, ok := m["width"]; ok && wOpt.K == value.Number {
			nb.width = int(wOpt.N)
		}
		if hOpt, ok := m["height"]; ok && hOpt.K == value.Number {
			nb.height = int(hOpt.N)
		}
	}

	err := chromedp.Run(nb.ctx, chromedp.EmulateViewport(int64(nb.width), int64(nb.height)))
	if err != nil {
		nb.err = err
	}

	return nb
}

func (b *Browser) Launch(opts ...value.Value) *Browser {
	return b.New(opts...)
}

func (b *Browser) Viewport(args ...value.Value) *Browser {
	b.ensureCtx()
	if b.err != nil {
		return b
	}
	if len(args) == 0 {
		return b
	}

	firstArg := args[0]
	if firstArg.K == value.Map {
		m := firstArg.Map()
		if wOpt, ok := m["width"]; ok && wOpt.K == value.Number {
			b.width = int(wOpt.N)
		}
		if hOpt, ok := m["height"]; ok && hOpt.K == value.Number {
			b.height = int(hOpt.N)
		}
	} else if firstArg.K == value.Number && len(args) > 1 && args[1].K == value.Number {
		b.width = int(firstArg.N)
		b.height = int(args[1].N)
	}

	err := chromedp.Run(b.ctx, chromedp.EmulateViewport(int64(b.width), int64(b.height)))
	if err != nil {
		b.err = err
	}
	return b
}


func (b *Browser) ensureCtx() {
	if b.ctx == nil && b.err == nil {
		b.initCtx()
	}
}

func (b *Browser) initCtx() {
	initGlobalChrome()
	globalChromeMu.Lock()
	defer globalChromeMu.Unlock()

	if globalChromeCtx == nil {
		b.err = fmt.Errorf("chrome not initialized")
		return
	}

	b.ctx, b.cancel = chromedp.NewContext(globalChromeCtx)
	b.width = 1280
	b.height = 720

	err := chromedp.Run(b.ctx, chromedp.EmulateViewport(int64(b.width), int64(b.height)))
	if err != nil {
		b.err = err
	}
}

func (b *Browser) Close() {
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
}

func (b *Browser) Navigate(urlVal value.Value, opts ...value.Value) *Browser {
	b.ensureCtx()
	if b.err != nil {
		return b
	}
	urlStr := urlVal.Text()
	err := chromedp.Run(b.ctx, chromedp.Navigate(urlStr))
	if err != nil {
		b.err = err
		return b
	}

	// If there are options, e.g. { wait: 1000 } or { wait: "#msg" }
	if len(opts) > 0 && opts[0].K == value.Map {
		m := opts[0].Map()
		if waitOpt, ok := m["wait"]; ok {
			if waitOpt.K == value.Number {
				delay := time.Duration(waitOpt.N) * time.Millisecond
				time.Sleep(delay)
			} else if waitOpt.K == value.String {
				selector := waitOpt.Text()
				err := chromedp.Run(b.ctx, chromedp.WaitVisible(selector, chromedp.ByQuery))
				if err != nil {
					b.err = err
				}
			}
		}
	}
	return b
}

func (b *Browser) Click(selectorVal value.Value) *Browser {
	b.ensureCtx()
	if b.err != nil {
		return b
	}
	selector := selectorVal.Text()
	err := chromedp.Run(b.ctx, chromedp.Click(selector, chromedp.ByQuery))
	if err != nil {
		b.err = err
	}
	return b
}

func (b *Browser) Fill(selectorVal value.Value, textVal value.Value) *Browser {
	b.ensureCtx()
	if b.err != nil {
		return b
	}
	selector := selectorVal.Text()
	text := textVal.Text()
	err := chromedp.Run(b.ctx, chromedp.SetValue(selector, text, chromedp.ByQuery))
	if err != nil {
		b.err = err
	}
	return b
}

func (b *Browser) Type(selectorVal value.Value, textVal value.Value) *Browser {
	b.ensureCtx()
	if b.err != nil {
		return b
	}
	selector := selectorVal.Text()
	text := textVal.Text()
	err := chromedp.Run(b.ctx, chromedp.SendKeys(selector, text, chromedp.ByQuery))
	if err != nil {
		b.err = err
	}
	return b
}

func (b *Browser) Wait(val value.Value) *Browser {
	b.ensureCtx()
	if b.err != nil {
		return b
	}
	if val.K == value.Number {
		delay := time.Duration(val.N) * time.Millisecond
		time.Sleep(delay)
	} else {
		selector := val.Text()
		err := chromedp.Run(b.ctx, chromedp.WaitVisible(selector, chromedp.ByQuery))
		if err != nil {
			b.err = err
		}
	}
	return b
}

func (b *Browser) ResetError() *Browser {
	b.err = nil
	return b
}

func (b *Browser) HTML(selectorVal ...value.Value) value.Value {
	b.ensureCtx()
	if b.err != nil {
		return value.Value{K: value.Invalid, V: b.err.Error()}
	}
	var html string
	var err error
	if len(selectorVal) > 0 {
		selector := selectorVal[0].Text()
		err = chromedp.Run(b.ctx, chromedp.OuterHTML(selector, &html, chromedp.ByQuery))
	} else {
		err = chromedp.Run(b.ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery))
	}
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(html)
}

func (b *Browser) Text(selectorVal value.Value) value.Value {
	b.ensureCtx()
	if b.err != nil {
		return value.Value{K: value.Invalid, V: b.err.Error()}
	}
	selector := selectorVal.Text()
	var text string
	err := chromedp.Run(b.ctx, chromedp.Text(selector, &text, chromedp.ByQuery))
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(text)
}

func (b *Browser) Value(selectorVal value.Value) value.Value {
	b.ensureCtx()
	if b.err != nil {
		return value.Value{K: value.Invalid, V: b.err.Error()}
	}
	selector := selectorVal.Text()
	var val string
	err := chromedp.Run(b.ctx, chromedp.Value(selector, &val, chromedp.ByQuery))
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(val)
}

func (b *Browser) Screenshot() value.Value {
	b.ensureCtx()
	if b.err != nil {
		return value.Value{K: value.Invalid, V: b.err.Error()}
	}
	var buf []byte
	err := chromedp.Run(b.ctx, chromedp.CaptureScreenshot(&buf))
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(buf)
}

func (b *Browser) Evaluate(scriptVal value.Value) value.Value {
	b.ensureCtx()
	if b.err != nil {
		return value.Value{K: value.Invalid, V: b.err.Error()}
	}
	script := scriptVal.Text()
	var res interface{}
	err := chromedp.Run(b.ctx, chromedp.Evaluate(script, &res))
	if err != nil {
		return value.Value{K: value.Invalid, V: err.Error()}
	}
	return value.New(res)
}

func (b *Browser) Err() value.Value {
	if b.err != nil {
		return value.New(b.err.Error())
	}
	return value.NewNil()
}

func (b *Browser) Goto(urlVal value.Value, opts ...value.Value) *Browser {
	return b.Navigate(urlVal, opts...)
}

func (b *Browser) TextContent(selectorVal value.Value) value.Value {
	return b.Text(selectorVal)
}

func (b *Browser) InnerHTML(selectorVal ...value.Value) value.Value {
	return b.HTML(selectorVal...)
}

func (b *Browser) NewPage(args ...value.Value) *Browser {
	b.ensureCtx()
	if b.err != nil {
		return b
	}

	if len(args) == 0 {
		return b
	}

	arg := args[0]
	if arg.K == value.String {
		// If it's a string, treat it as a URL and navigate immediately
		b.Navigate(arg)
	} else if arg.K == value.Map {
		m := arg.Map()
		// Viewport configuration
		var w, h int
		if wOpt, ok := m["width"]; ok && wOpt.K == value.Number {
			w = int(wOpt.N)
		}
		if hOpt, ok := m["height"]; ok && hOpt.K == value.Number {
			h = int(hOpt.N)
		}
		if w > 0 || h > 0 {
			if w == 0 {
				w = b.width
			}
			if h == 0 {
				h = b.height
			}
			b.width = w
			b.height = h
			err := chromedp.Run(b.ctx, chromedp.EmulateViewport(int64(w), int64(h)))
			if err != nil {
				b.err = err
			}
		}
		// Navigation URL configuration inside options
		if urlOpt, ok := m["url"]; ok {
			b.Navigate(urlOpt)
		}
	}
	return b
}

func (b *Browser) Page(args ...value.Value) *Browser {
	return b.NewPage(args...)
}

