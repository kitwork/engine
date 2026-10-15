package engine

import (
	"fmt"
	"strings"

	"github.com/kitwork/engine/value"
)

// Launch is a compatibility shorthand for desktop.launch.
func (b *ServerBuilder) Launch(args ...value.Value) *ServerBuilder {
	if len(args) != 1 {
		b.err = "app.launch expects one object or false"
		return b
	}
	if _, exists := b.config["launch"]; exists {
		b.err = "app.launch may only be declared once"
		return b
	}
	if desktop, ok := b.config["desktop"]; ok && desktop.IsMap() {
		if _, exists := desktop.Map()["launch"]; exists {
			b.err = "cannot combine app.launch with desktop.launch"
			return b
		}
	}
	normalized, err := normalizeLaunchOption(args[0])
	if err != nil {
		b.err = "app.launch: " + err.Error()
		return b
	}
	b.config["launch"] = normalized
	return b
}

func normalizeLaunchOption(option value.Value) (value.Value, error) {
	if option.K == value.Bool {
		return option, nil
	}
	if !option.IsMap() {
		return value.NULL, fmt.Errorf("expected boolean or launch options object")
	}
	fields := option.Map()
	for key, item := range fields {
		switch key {
		case "position":
			if err := validateDesktopPosition(item); err != nil {
				return value.NULL, fmt.Errorf("position: %w", err)
			}
		case "glass":
			if item.K != value.Bool {
				return value.NULL, fmt.Errorf("glass must be boolean")
			}
		case "background":
			if err := validateDesktopColor(item); err != nil {
				return value.NULL, fmt.Errorf("background: %w", err)
			}
		case "width", "height":
			min, max := 240.0, 2048.0
			if key == "height" {
				min, max = 120, 1200
			}
			if item.K != value.Number || !(item.N >= min && item.N <= max) || item.N != float64(int(item.N)) {
				return value.NULL, fmt.Errorf("%s must be an integer between %.0f and %.0f", key, min, max)
			}
		case "title", "message":
			if item.K != value.String || strings.TrimSpace(item.Text()) == "" || len(item.Text()) > 256 || strings.ContainsRune(item.Text(), 0) {
				return value.NULL, fmt.Errorf("%s must be nonempty text at most 256 bytes", key)
			}
		case "rounded":
			if item.K != value.Number || !(item.N >= 0 && item.N <= 128) || item.N != float64(int(item.N)) {
				return value.NULL, fmt.Errorf("rounded must be an integer between 0 and 128")
			}
		case "template":
			if item.K != value.String {
				return value.NULL, fmt.Errorf("template must be an HTML filename")
			}
			if _, err := normalizeSplashOption(item); err != nil {
				return value.NULL, err
			}
		case "bind":
			if !item.IsMap() {
				return value.NULL, fmt.Errorf("bind must be an object")
			}
			budget := 10000
			if err := validateSplashData(item, 0, &budget); err != nil {
				return value.NULL, err
			}
		default:
			return value.NULL, fmt.Errorf("unknown option %q", key)
		}
	}
	return option, nil
}
