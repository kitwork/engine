// Command shot renders one page in a headless browser and writes a PNG — in the
// light or the dark theme, at a chosen width — so a visual change can be proved
// in both skins without a desktop session's OS preference getting in the way.
//
//	go run ./cmd/shot -url http://localhost:8101/components/button -theme light -out button-light.png
//	go run ./cmd/shot -url http://localhost:8101/components -theme dark -full -out components-dark.png
//
// The theme is emulated through prefers-color-scheme in a fresh profile, which
// is what a site's pre-paint script reads when nothing is stored yet.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func main() {
	url := flag.String("url", "", "page to render")
	out := flag.String("out", "shot.png", "PNG to write")
	width := flag.Int("width", 1440, "viewport width")
	height := flag.Int("height", 900, "viewport height")
	theme := flag.String("theme", "light", "light | dark")
	full := flag.Bool("full", false, "capture the whole page, not just the viewport")
	wait := flag.Duration("wait", 800*time.Millisecond, "settle time after load")
	flag.Parse()
	if *url == "" {
		fmt.Fprintln(os.Stderr, "shot: -url is required")
		os.Exit(2)
	}
	if *theme != "light" && *theme != "dark" {
		fmt.Fprintln(os.Stderr, "shot: -theme must be light or dark")
		os.Exit(2)
	}

	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(*width, *height),
		chromedp.Flag("hide-scrollbars", true),
	)
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	defer cancelAllocator()
	ctx, cancel := chromedp.NewContext(allocator)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 60*time.Second)
	defer cancelTimeout()

	var png []byte
	shoot := chromedp.CaptureScreenshot(&png)
	if *full {
		shoot = chromedp.FullScreenshot(&png, 100)
	}
	err := chromedp.Run(ctx,
		emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
			{Name: "prefers-color-scheme", Value: *theme},
		}),
		chromedp.EmulateViewport(int64(*width), int64(*height)),
		chromedp.Navigate(*url),
		chromedp.Sleep(*wait),
		shoot,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shot:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, png, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "shot:", err)
		os.Exit(1)
	}
	fmt.Printf("%s ← %s (%s, %dpx)\n", *out, *url, *theme, *width)
}
