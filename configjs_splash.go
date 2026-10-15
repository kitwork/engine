package engine

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/value"
)

// Splash declares loading presentation without creating another app surface.
func (b *ServerBuilder) Splash(args ...value.Value) *ServerBuilder {
	return b.localPresentation("splash", args...)
}

// Offline declares a local connection-recovery document, not an offline cache.
func (b *ServerBuilder) Offline(args ...value.Value) *ServerBuilder {
	return b.localPresentation("offline", args...)
}

func (b *ServerBuilder) localPresentation(kind string, args ...value.Value) *ServerBuilder {
	if len(args) < 1 || len(args) > 2 {
		b.err = "app." + kind + " expects an option, or a template filename and bind object"
		return b
	}
	if _, exists := b.config[kind]; exists {
		b.err = "app." + kind + " may only be declared once"
		return b
	}
	if desktop, ok := b.config["desktop"]; ok && desktop.IsMap() {
		if _, exists := desktop.Map()[kind]; exists {
			b.err = "cannot combine app." + kind + " with desktop." + kind
			return b
		}
	}
	option := args[0]
	if len(args) == 2 {
		if option.K != value.String {
			b.err = "app." + kind + "(template, bind) requires a template filename"
			return b
		}
		option = value.New(map[string]value.Value{"template": option, "bind": args[1]})
	}
	normalized, err := normalizeSplashOption(option, kind)
	if err != nil {
		b.err = "app." + kind + ": " + err.Error()
		return b
	}
	b.config[kind] = normalized
	return b
}

func normalizeSplashOption(option value.Value, defaults ...string) (value.Value, error) {
	isSplash := len(defaults) > 0 && defaults[0] == "splash"
	if option.K == value.Number && isSplash {
		option = value.New(map[string]value.Value{"duration": option})
	}
	if option.K == value.Bool {
		return option, nil
	}
	validatePath := func(path value.Value) error {
		if path.K != value.String {
			return fmt.Errorf("template must be a relative HTML filename")
		}
		name := path.Text()
		clean := filepath.Clean(name)
		if name == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || !strings.EqualFold(filepath.Ext(clean), ".html") {
			return fmt.Errorf("filename must be a relative .html path inside the manifest directory")
		}
		return nil
	}
	if option.K == value.String {
		return option, validatePath(option)
	}
	if !option.IsMap() {
		return value.NULL, fmt.Errorf("expected boolean, filename, or { template, bind }")
	}
	fields := option.Map()
	for key := range fields {
		if key != "template" && key != "bind" && !(isSplash && key == "duration") {
			return value.NULL, fmt.Errorf("unknown option %q", key)
		}
	}
	_, hasTemplate := fields["template"]
	if hasTemplate || len(defaults) == 0 {
		if err := validatePath(fields["template"]); err != nil {
			return value.NULL, err
		}
	}
	bind, exists := fields["bind"]
	if !exists {
		bind = value.New(map[string]value.Value{})
	}
	if !bind.IsMap() {
		return value.NULL, fmt.Errorf("bind must be an object")
	}
	budget := 10000
	if err := validateSplashData(bind, 0, &budget); err != nil {
		return value.NULL, err
	}
	result := map[string]value.Value{"bind": bind}
	if duration, exists := fields["duration"]; exists {
		if duration.K != value.Number || math.IsNaN(duration.N) || math.IsInf(duration.N, 0) || duration.N < 0 || duration.N > 60 {
			return value.NULL, fmt.Errorf("duration must be seconds from 0 to 60")
		}
		result["duration"] = duration
	}
	if hasTemplate {
		result["template"] = fields["template"]
	}
	return value.New(result), nil
}

func validateSplashData(item value.Value, depth int, budget *int) error {
	*budget--
	if depth > 32 || *budget < 0 {
		return fmt.Errorf("bind exceeds its depth or value limit")
	}
	switch item.K {
	case value.Nil, value.String, value.Bool:
		return nil
	case value.Number:
		if !math.IsNaN(item.N) && !math.IsInf(item.N, 0) {
			return nil
		}
	case value.Map:
		for _, child := range item.Map() {
			if err := validateSplashData(child, depth+1, budget); err != nil {
				return err
			}
		}
		return nil
	case value.Array:
		for _, child := range item.Array() {
			if err := validateSplashData(child, depth+1, budget); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("bind accepts JSON data only, not %s", item.K.String())
}
