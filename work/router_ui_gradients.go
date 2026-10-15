package work

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kitwork/engine/ui/stylesheet"
	"github.com/kitwork/engine/value"
)

// Gradients declares the site's own gradients for its .ui pages, each written one explicit way
// (decision of 12/10/2026, ui/stylesheet/gradients.go):
//
//	router.gradients({
//	  hero: { type: "linear", direction: "to-bottom-end", stops: [ { color: "brand", at: 0 }, { color: "violet", at: 100 } ] },
//	  halo: { type: "radial", position: "top", stops: [ { color: "brand/40", at: 0 }, { color: "brand/0", at: 70 } ] },
//	  sky: { type: "linear", direction: "to-bottom", stops: [ { color: { light: "#849cdd", dark: "#202f5e" }, at: 0 }, { color: "canvas", at: 100 } ] }
//	});
//
// A stop's color is a colour as color= takes it, or its own colour in each mode, { light, dark }, as
// router.colors writes a token (decision D2 of 13/10/2026). A page then writes background="hero" or
// color="hero" (gradient text), and any gradient lighter or stronger: background="hero-80" (every stop
// 80% of the way toward canvas), background="hero+20" (every stop 20% further from canvas, per mode).
// The standard set (fade-top … prism) and the page backdrops (cirrus … twilight) need no declaring.
// Like router.colors, a branch's gradients are its folder's and the ones below, over the site's, the
// nearest folder winning per name, and several calls in one file add up.
// What can be read from the call alone is checked at once and stops the boot: the shape (an object of
// gradients, each with type, direction or position, and stops of { color, at }), the words, the order
// of the stops, the name. Whether each stop's colour is one the route's palette names, and whether the
// name is a token of it, is answered by the route's pages, as a router.colors value is.
func (f *FolderRouter) Gradients(args ...value.Value) (*FolderRouter, error) {
	if len(args) != 1 || !args[0].IsMap() {
		return f, fmt.Errorf(`router.gradients nhận một object các gradient: router.gradients({ hero: { type: "linear", direction: "to-bottom-end", stops: [ { color: "brand", at: 0 }, { color: "brand+15", at: 100 } ] } })`)
	}
	declared := args[0].Map()
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g, err := gradientOf(name, declared[name])
		if err != nil {
			return f, err
		}
		if err := stylesheet.CheckGradient(name, g); err != nil {
			return f, err
		}
	}
	if f == nil {
		return f, nil
	}
	f.gradients = addDeclarations(f.gradients, declared)
	return f, nil
}

// gradientOf reads one gradient of router.gradients as the stylesheet takes it, refusing a shape that
// is not one: an object of type, direction, position and stops, the stops an array of { color, at },
// color a string and at a number.
func gradientOf(name string, v value.Value) (stylesheet.Gradient, error) {
	var g stylesheet.Gradient
	at := "router.gradients({ " + name + ": … })"
	if !v.IsMap() {
		return g, fmt.Errorf(`%s: một gradient là một object { type, direction hay position, stops }, không phải %s`, at, v.String())
	}
	fields := v.Map()
	for _, key := range sortedNames(fields) {
		field := fields[key]
		switch key {
		case "type", "direction", "position":
			if !field.IsString() {
				return g, fmt.Errorf(`%s: %s là một chữ ("linear", "to-bottom", "top"…)`, at, key)
			}
			switch key {
			case "type":
				g.Type = strings.TrimSpace(field.String())
			case "direction":
				g.Direction = strings.TrimSpace(field.String())
			default:
				g.Position = strings.TrimSpace(field.String())
			}
		case "stops":
			if !field.IsArray() {
				return g, fmt.Errorf(`%s: stops là một mảng các điểm dừng: [ { color: "brand", at: 0 }, { color: "brand+15", at: 100 } ]`, at)
			}
			for i, stop := range field.Array() {
				s, err := stopOf(at, i+1, stop)
				if err != nil {
					return g, err
				}
				g.Stops = append(g.Stops, s)
			}
		default:
			return g, fmt.Errorf(`%s: một gradient nhận type, direction, position và stops, không có "%s"`, at, key)
		}
	}
	return g, nil
}

// stopOf reads one stop of a gradient, { color, at }: color a colour as color= takes it, or the stop's
// own colour in each mode, { light, dark }, exactly as router.colors writes a token (decision D2 of
// 13/10/2026). A pair reads light and dark only, each a string: a mistyped key (drak) would leave the
// stop without its dark. Whether each side is a colour is stylesheet.CheckGradient's.
func stopOf(at string, place int, v value.Value) (stylesheet.Stop, error) {
	var s stylesheet.Stop
	if !v.IsMap() {
		return s, fmt.Errorf(`%s: điểm dừng %d là một object { color, at }: { color: "brand", at: 0 }`, at, place)
	}
	fields := v.Map()
	for _, key := range sortedNames(fields) {
		if key != "color" && key != "at" {
			return s, fmt.Errorf(`%s: điểm dừng %d nhận color và at, không có "%s"`, at, place, key)
		}
	}
	color, hasColor := fields["color"]
	position, hasAt := fields["at"]
	switch {
	case hasColor && color.IsMap():
	case !hasColor || !color.IsString():
		return s, fmt.Errorf(`%s: điểm dừng %d thiếu color, một màu viết như color= nhận (brand, ink-30, paper/0, white, tomato) hoặc một cặp { light: "#849cdd", dark: "#202f5e" }`, at, place)
	}
	if !hasAt || !position.IsNumber() {
		return s, fmt.Errorf(`%s: điểm dừng %d thiếu at, một số từ 0 đến 100 (vị trí trên gradient)`, at, place)
	}
	if !color.IsMap() {
		return stylesheet.Stop{Color: strings.TrimSpace(color.String()), At: position.Float()}, nil
	}
	pair := color.Map()
	for _, key := range sortedNames(pair) {
		if key != "light" && key != "dark" {
			return s, fmt.Errorf(`%s: điểm dừng %d: một cặp màu chỉ nhận light và dark, không có "%s"`, at, place, key)
		}
		if !pair[key].IsString() {
			return s, fmt.Errorf(`%s: điểm dừng %d: %s của cặp màu là một chữ, "#rrggbb" hay một màu có tên ({ light: "#849cdd", dark: "#202f5e" })`, at, place, key)
		}
	}
	s = stylesheet.Stop{At: position.Float()}
	if light, ok := pair["light"]; ok {
		s.Pair.Light = strings.TrimSpace(light.String())
	}
	if dark, ok := pair["dark"]; ok {
		s.Pair.Dark = strings.TrimSpace(dark.String())
	}
	if s.Pair == (stylesheet.Color{}) {
		return s, fmt.Errorf(`%s: điểm dừng %d: cặp màu trống, viết { light: "#849cdd", dark: "#202f5e" }`, at, place)
	}
	return s, nil
}

// withRouteGradients is a route's palette with the router.gradients() of every folder from the site
// root down to the route, the nearest last (stylesheet.Palette.WithGradients: the nearest wins per name,
// and each stop's colour is checked against the route's palette).
func withRouteGradients(palette *stylesheet.Palette, node *RouteNode) (*stylesheet.Palette, error) {
	var folders []*FolderRouter
	for n := node; n != nil; n = n.parent {
		if n.folder != nil && len(n.folder.gradients) > 0 {
			folders = append(folders, n.folder)
		}
	}
	chain := make([]map[string]stylesheet.Gradient, 0, len(folders))
	for i := len(folders) - 1; i >= 0; i-- {
		layer := map[string]stylesheet.Gradient{}
		for name, v := range folders[i].gradients {
			g, err := gradientOf(name, v) // checked when declared: it reads
			if err != nil {
				return nil, err
			}
			layer[name] = g
		}
		chain = append(chain, layer)
	}
	return palette.WithGradients(chain...)
}
