package ui

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/gif" // DecodeConfig reads the header of each format it knows
	_ "image/jpeg"
	_ "image/png"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The size of a static image of the site, read when the page compiles (KITWORK_UI §7, "Kích thước,
// đệm theo cạnh và tỉ lệ khung"): written on <img> as width and height, the browser keeps the image's
// room before the file arrives, so nothing under it jumps. Only a path in the site (/assets/…) whose
// file can be read is measured; an image from data, from another host or a data: URL is left alone.

// maxImageHeader is how much of a file is read to find its size: every format here says it in its
// first bytes (an SVG in its root tag).
const maxImageHeader = 64 << 10

// imageSize is the width and height of the site's file a src names, in CSS pixels.
func (c *compiler) imageSize(src string) (int, int, bool) {
	if !strings.HasPrefix(src, "/") || strings.HasPrefix(src, "//") || strings.ContainsAny(src, "?#{") {
		return 0, 0, false
	}
	clean := filepath.Clean(filepath.FromSlash(src))
	if strings.Contains(clean, "..") {
		return 0, 0, false
	}
	data, err := c.site.readHead(filepath.Join(c.site.Root, clean), maxImageHeader)
	if err != nil || len(data) == 0 {
		return 0, 0, false
	}
	switch strings.ToLower(filepath.Ext(clean)) {
	case ".svg":
		return svgSize(data)
	case ".webp":
		return webpSize(data)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

var (
	svgRoot    = regexp.MustCompile(`(?is)<svg\b[^>]*>`)
	svgLength  = regexp.MustCompile(`^\s*([0-9]*\.?[0-9]+)\s*(px)?\s*$`)
	svgNumbers = regexp.MustCompile(`[-+]?[0-9]*\.?[0-9]+(?:[eE][-+]?[0-9]+)?`)
)

// svgSize reads the root <svg>: its width and height when both are plain lengths (28, 28px), else the
// size of its viewBox, the ratio the drawing keeps.
func svgSize(data []byte) (int, int, bool) {
	root := svgRoot.Find(data)
	if root == nil {
		return 0, 0, false
	}
	attribute := func(name string) string {
		m := regexp.MustCompile(`(?i)\s` + name + `\s*=\s*["']([^"']*)["']`).FindSubmatch(root)
		if m == nil {
			return ""
		}
		return string(m[1])
	}
	width, height := svgLength.FindStringSubmatch(attribute("width")), svgLength.FindStringSubmatch(attribute("height"))
	if width != nil && height != nil {
		w, _ := strconv.ParseFloat(width[1], 64)
		h, _ := strconv.ParseFloat(height[1], 64)
		if w > 0 && h > 0 {
			return int(math.Round(w)), int(math.Round(h)), true
		}
	}
	box := svgNumbers.FindAllString(attribute("viewBox"), -1)
	if len(box) != 4 {
		return 0, 0, false
	}
	w, _ := strconv.ParseFloat(box[2], 64)
	h, _ := strconv.ParseFloat(box[3], 64)
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return int(math.Round(w)), int(math.Round(h)), true
}

// webpSize reads a WebP header: RIFF, WEBP, then a lossy (VP8 ), lossless (VP8L) or extended (VP8X)
// chunk, each saying the size its own way.
func webpSize(data []byte) (int, int, bool) {
	if len(data) < 30 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, false
	}
	switch string(data[12:16]) {
	case "VP8 ":
		// the frame tag (3 bytes) and the start code 9d 01 2a, then 14-bit width and height
		if data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, false
		}
		w := int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
		return w, h, w > 0 && h > 0
	case "VP8L":
		if data[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, true
	case "VP8X":
		w := int(uint32(data[24])|uint32(data[25])<<8|uint32(data[26])<<16) + 1
		h := int(uint32(data[27])|uint32(data[28])<<8|uint32(data[29])<<16) + 1
		return w, h, true
	}
	return 0, 0, false
}
