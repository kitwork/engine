package qrcode

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io/ioutil"
	"path"
	"sort"
	"strings"
)

func (o *Options) ExtractLogoColors() ([]string, error) {
	logoPath := o.Center.Image
	if logoPath == "" {
		logoPath = o.Center.Logo
	}
	if logoPath == "" {
		return nil, fmt.Errorf("no logo path configured")
	}

	var img image.Image

	if strings.HasPrefix(logoPath, "data:image/") {
		parts := strings.Split(logoPath, ",")
		base64Data := parts[len(parts)-1]
		decoded, err := base64.StdEncoding.DecodeString(base64Data)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 logo: %w", err)
		}
		imgDec, _, errDec := image.Decode(bytes.NewReader(decoded))
		if errDec != nil {
			return nil, fmt.Errorf("failed to decode base64 image: %w", errDec)
		}
		img = imgDec
	} else {
		pngData, err := ioutil.ReadFile(logoPath)
		if err != nil {
			altPath := path.Join("resource", "assets", "images", "logo", logoPath+".png")
			var errAlt error
			pngData, errAlt = ioutil.ReadFile(altPath)
			if errAlt != nil {
				altPath2 := path.Join("resource", "assets", "images", "logo", logoPath)
				var errAlt2 error
				pngData, errAlt2 = ioutil.ReadFile(altPath2)
				if errAlt2 != nil {
					return nil, fmt.Errorf("failed to read logo file: %w", errAlt2)
				}
			}
		}
		imgDec, _, errDec := image.Decode(bytes.NewReader(pngData))
		if errDec != nil {
			return nil, fmt.Errorf("failed to decode logo image: %w", errDec)
		}
		img = imgDec
	}

	colorCount := make(map[string]int)
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixelColor := img.At(x, y)
			r, g, b, a := pixelColor.RGBA()

			// Skip transparent or near-transparent pixels (a < 1000 out of 65535)
			if a < 1000 {
				continue
			}

			// Convert to standard 8-bit RGBA
			rgbaColor := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}

			// Skip near-white colors (threshold 225)
			if rgbaColor.R >= 225 && rgbaColor.G >= 225 && rgbaColor.B >= 225 {
				continue
			}

			hexColor := fmt.Sprintf("#%02x%02x%02x", rgbaColor.R, rgbaColor.G, rgbaColor.B)
			colorCount[hexColor]++
		}
	}

	type colorItem struct {
		Color string
		Count int
	}
	var list []colorItem
	for col, count := range colorCount {
		list = append(list, colorItem{Color: col, Count: count})
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Count > list[j].Count
	})

	var result []string
	for _, item := range list {
		result = append(result, item.Color)
	}

	return result, nil
}

func getBase64Image(filePath string) string {
	if strings.HasPrefix(filePath, "data:image/") {
		return filePath
	}

	pngData, err := ioutil.ReadFile(filePath)
	if err != nil {
		altPath := path.Join("resource", "assets", "images", "logo", filePath+".png")
		pngData, err = ioutil.ReadFile(altPath)
		if err != nil {
			altPath2 := path.Join("resource", "assets", "images", "logo", filePath)
			pngData, err = ioutil.ReadFile(altPath2)
			if err != nil {
				return ""
			}
		}
	}

	var mimeType string
	if strings.HasSuffix(strings.ToLower(filePath), ".jpg") || strings.HasSuffix(strings.ToLower(filePath), ".jpeg") {
		mimeType = "image/jpeg"
	} else if strings.HasSuffix(strings.ToLower(filePath), ".gif") {
		mimeType = "image/gif"
	} else {
		mimeType = "image/png"
	}

	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(pngData))
}
