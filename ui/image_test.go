package ui

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// imageSite is a site whose content shows one image, with the site's files beside it.
func imageSite(t *testing.T, tag string, files map[string][]byte) string {
	t.Helper()
	sources := map[string][]byte{"/site/content.ui": []byte(`<content>` + tag + `</content>`)}
	for name, data := range files {
		sources[name] = data
	}
	s := &Site{Root: "/site", Reload: true, Read: func(path string) ([]byte, error) {
		if data, ok := sources[filepath.ToSlash(path)]; ok {
			return data, nil
		}
		return nil, os.ErrNotExist
	}}
	return renderAt(t, s, ".", "/", nil)
}

func encoded(t *testing.T, write func(*bytes.Buffer, image.Image) error, width, height int) []byte {
	t.Helper()
	picture := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{color.Black, color.White})
	var b bytes.Buffer
	if err := write(&b, picture); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// webp builds the first bytes of a WebP file of each kind: lossy (VP8 ), lossless (VP8L), extended
// (VP8X), each saying its size its own way.
func webp(kind string, width, height int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBP" + kind + "\x00\x00\x00\x00")
	switch kind {
	case "VP8 ":
		b = append(b, 0, 0, 0, 0x9d, 0x01, 0x2a)
		b = binary.LittleEndian.AppendUint16(b, uint16(width))
		b = binary.LittleEndian.AppendUint16(b, uint16(height))
	case "VP8L":
		b = append(b, 0x2f)
		b = binary.LittleEndian.AppendUint32(b, uint32(width-1)|uint32(height-1)<<14)
	case "VP8X":
		b = append(b, 0, 0, 0, 0)
		w, h := width-1, height-1
		b = append(b, byte(w), byte(w>>8), byte(w>>16), byte(h), byte(h>>8), byte(h>>16))
	}
	return append(b, make([]byte, 8)...)
}

// The engine writes width and height on an <img> whose file it can read when the page compiles: an
// SVG's own width and height (else its viewBox), a PNG, JPEG or GIF by the stdlib's DecodeConfig, a
// WebP by its header. The browser keeps the image's room before the file arrives, so nothing under it
// jumps. CONTROL: an image from data, from another host, a data: URL, a file that is not there, a path
// that climbs out of the site, a file that is no image: none is measured.
func TestImageSizeFromTheSitesFile(t *testing.T) {
	gifData := encoded(t, func(b *bytes.Buffer, m image.Image) error { return gif.Encode(b, m, nil) }, 17, 9)
	cases := []struct {
		name, file string
		data       []byte
		want       string
	}{
		{"svg width and height", "/site/assets/logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="28" height="28px" viewBox="0 0 56 56"></svg>`), `width="28" height="28"`},
		{"svg viewBox", "/site/assets/hero.svg", []byte("<?xml version=\"1.0\"?>\n<svg xmlns=\"http://www.w3.org/2000/svg\"\n viewBox=\"0 0 1200 520\" role=\"img\"></svg>"), `width="1200" height="520"`},
		{"svg percentage falls back to viewBox", "/site/assets/wide.svg", []byte(`<svg width="100%" height="100%" viewBox="0 0 300 100"></svg>`), `width="300" height="100"`},
		{"png", "/site/assets/a.png", encoded(t, func(b *bytes.Buffer, m image.Image) error { return png.Encode(b, m) }, 640, 360), `width="640" height="360"`},
		{"jpeg", "/site/assets/a.jpg", encoded(t, func(b *bytes.Buffer, m image.Image) error { return jpeg.Encode(b, m, nil) }, 30, 20), `width="30" height="20"`},
		{"gif", "/site/assets/a.gif", gifData, `width="17" height="9"`},
		{"webp lossy", "/site/assets/a.webp", webp("VP8 ", 400, 300), `width="400" height="300"`},
		{"webp lossless", "/site/assets/b.webp", webp("VP8L", 123, 45), `width="123" height="45"`},
		{"webp extended", "/site/assets/c.webp", webp("VP8X", 1920, 1080), `width="1920" height="1080"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := strings.TrimPrefix(c.file, "/site")
			html := imageSite(t, `<image value="`+src+`" alt=""></image>`, map[string][]byte{c.file: c.data})
			mustContain(t, html, `<img src="`+src+`" alt="" `+c.want+` loading="lazy" decoding="async">`)
		})
	}

	svg := map[string][]byte{"/site/assets/logo.svg": []byte(`<svg width="28" height="28"></svg>`), "/etc/logo.svg": []byte(`<svg width="9" height="9"></svg>`),
		"/site/assets/note.svg": []byte(`not an image`)}
	for _, tag := range []string{
		`<image value="{{ picture }}" alt=""></image>`,                    // from data
		`<image value="https://cdn.test/assets/logo.svg" alt=""></image>`, // another host
		`<image value="//cdn.test/assets/logo.svg" alt=""></image>`,
		`<image value="/assets/missing.svg" alt=""></image>`,  // not there
		`<image value="/../etc/logo.svg" alt=""></image>`,     // out of the site
		`<image value="/assets/logo.svg?v=2" alt=""></image>`, // a query: not the file as it is
		`<image value="/assets/note.svg" alt=""></image>`,     // no image in it
		`<image value="assets/logo.svg" alt=""></image>`,      // relative: no file the page can name
	} {
		html := imageSite(t, tag, svg)
		if strings.Contains(html, ` width="`) || strings.Contains(html, ` height="`) {
			t.Errorf("%s: no size may be written:\n%s", tag, html)
		}
	}
	// control: the same file by its site path is measured
	mustContain(t, imageSite(t, `<image value="/assets/logo.svg" alt=""></image>`, svg), `width="28" height="28"`)
}

// aspect-ratio gives a box its shape, with width prefixes (4/3 on a phone, 21/9 from large); on an image
// fit says how the picture fills it — cover is reboot.css's default once a ratio is written, contain
// shows it whole. The class is the value as written (aspect-ratio-4/3); square and auto are words.
func TestAspectRatioAndFit(t *testing.T) {
	html, css := renderCSS(t, `<content><image value="/hero.svg" alt="" aspect-ratio="4/3" large:aspect-ratio="21/9" fit="cover"></image>`+
		`<image value="/logo.svg" alt="" aspect-ratio="square" fit="contain"></image><card aspect-ratio="16/9"><text>x</text></card></content>`, nil)
	mustContain(t, html,
		`<img class="aspect-ratio-4/3 large:aspect-ratio-21/9 fit-cover"`,
		`<img class="aspect-ratio-square fit-contain"`,
		`<div class="card flex-column aspect-ratio-16/9">`)
	mustContain(t, css,
		`.aspect-ratio-4\/3{aspect-ratio:4/3}`,
		`.aspect-ratio-square{aspect-ratio:1}`,
		`.aspect-ratio-16\/9{aspect-ratio:16/9}`,
		`.fit-cover{object-fit:cover}`,
		`.fit-contain{object-fit:contain}`,
		"@media (min-width:1024px){\n"+`.large\:aspect-ratio-21\/9{aspect-ratio:21/9}`)
	mustContain(t, rebootCSS(), `:where(img[class*="aspect-ratio-"]){object-fit:cover}`)
	for source, says := range map[string]string{
		`<content><box fit="cove"><text>x</text></box></content>`:                      "fit",
		`<content><image value="/a.svg" alt="" aspect-ratio="16:9"></image></content>`: "aspect-ratio",
		`<content><image value="/a.svg" alt="" fit="fill"></image></content>`:          "fit",
	} {
		if got := compileError(t, map[string]string{"/site/content.ui": source}); !strings.HasPrefix(got, "content.ui:1:") || !strings.Contains(got, says) {
			t.Errorf("%s: %s", source, got)
		}
	}
}

// Sizes take a raw value with a unit of a closed set (px rem em % ch vw vh dvh), written as it is in
// the class (max-width-640px, width-50%, max-width-68ch), as well as a word or a step of the scale.
// Spacing off the scale is drawn as written and warns naming the nearest step (padding="13px"); colours
// stay tokens; calc() and var() are errors.
func TestRawSizesAndTheirUnits(t *testing.T) {
	html, css := renderCSS(t, `<content><box max-width="640px" width="50%" min-height="100dvh"><text max-width="68ch">x</text></box>`+
		`<section height="12.5rem" max-height="80vh" min-width="2em" width="100vw"><text>y</text></section></content>`, nil)
	mustContain(t, html,
		`<div class="flex-column max-width-640px width-50% min-height-100dvh">`,
		`<p class="max-width-68ch">`,
		`<section class="flex-column height-12.5rem max-height-80vh min-width-2em width-100vw">`)
	mustContain(t, css,
		`.max-width-640px{max-inline-size:640px}`,
		`.width-50\%{inline-size:50%}`,
		`.min-height-100dvh{min-block-size:100dvh}`,
		`.max-width-68ch{max-inline-size:68ch}`,
		`.height-12\.5rem{block-size:12.5rem}`)
	for source, says := range map[string]string{
		`<content><box width="calc(100% - 2rem)"><text>x</text></box></content>`: "không nhận calc() hay var()",
		`<content><box width="var(--x)"><text>x</text></box></content>`:          "không nhận calc() hay var()",
		`<content><box width="12pt"><text>x</text></box></content>`:              "px, rem, em, %, ch, vw, vh, dvh",
	} {
		got := compileError(t, map[string]string{"/site/content.ui": source})
		if !strings.HasPrefix(got, "content.ui:1:") || !strings.Contains(got, says) {
			t.Errorf("%s\n  %s\n  does not say %q", source, got, says)
		}
	}
	// padding="13px" is drawn as written and names the nearest step of the scale (3 is .75rem = 12px)
	if _, _, got := stylesheetDiagnose(t, map[string]string{"/site/content.ui": `<content><box padding="13px"><text>x</text></box></content>`}, nil); len(got) != 1 || !strings.Contains(got[0], `gần nhất là padding="3" (.75rem)`) {
		t.Errorf("padding=13px names the nearest step: %q", got)
	}
}

// The words .ui took from Tailwind are compile errors that teach the new word: radius md/lg/xl are
// small/medium/large (the same pixels), shadow md/lg are medium/large; a Tailwind colour (blue-500) is
// told it is Tailwind's spelling and how a named colour is made lighter or stronger (a token seeded by
// it, 13/10/2026), and a CSS colour name written with a hyphen is told its name (deeppink).
func TestTeachingErrors(t *testing.T) {
	for source, says := range map[string]string{
		`<content><box radius="md"><text>x</text></box></content>`:          `radius="md" là chữ cũ của Tailwind: viết radius="small" (.375rem)`,
		`<content><box radius="lg"><text>x</text></box></content>`:          `viết radius="medium" (.5rem)`,
		`<content><box radius="xl"><text>x</text></box></content>`:          `viết radius="large" (.75rem)`,
		`<content><box shadow="md"><text>x</text></box></content>`:          `shadow="md" là chữ cũ của Tailwind: viết shadow="medium"`,
		`<content><box hover:shadow="lg"><text>x</text></box></content>`:    `viết shadow="large"`,
		`<content><text color="blue-500">x</text></content>`:                `color="blue-500": blue-500 là cách viết của Tailwind; blue là một màu có tên, cố định: cần nhạt hơn hay đậm hơn thì cho một token màu ấy: router.colors({ accent: "blue" }) rồi viết accent-30, accent+15`,
		`<content><text color="deep-pink">x</text></content>`:               `color="deep-pink": deep-pink không phải tên màu: tên là deeppink (#ff1493)`,
		`<content><box background="red-500"><text>x</text></box></content>`: `background="red-500": red-500 là cách viết của Tailwind; red là một màu có tên`,
	} {
		got := compileError(t, map[string]string{"/site/content.ui": source})
		if !strings.HasPrefix(got, "content.ui:1:") || !strings.Contains(got, says) {
			t.Errorf("%s\n  %s\n  does not say %q", source, got, says)
		}
	}
	// control: the new words compile
	html := render(t, `<content><box radius="small" shadow="medium"><text color="ink-30">x</text></box></content>`, nil)
	mustContain(t, html, `class="flex-column radius-small shadow-medium"`, `class="color-ink-30"`)
}
