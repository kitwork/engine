package engine

import (
	"fmt"

	"github.com/kitwork/engine/value"
)

func validateDesktopColor(option value.Value) error {
	if option.K == value.String && option.Text() == "system" {
		return nil
	}
	if option.IsMap() {
		fields := option.Map()
		if len(fields) != 2 {
			return fmt.Errorf("expected light and dark colors")
		}
		for _, theme := range []string{"light", "dark"} {
			if err := validateDesktopHexColor(fields[theme]); err != nil {
				return fmt.Errorf("%s: %w", theme, err)
			}
		}
		return nil
	}
	return validateDesktopHexColor(option)
}

func validateDesktopHexColor(option value.Value) error {
	if option.K == value.String {
		text := option.Text()
		if len(text) == 7 && text[0] == '#' {
			valid := true
			for _, c := range text[1:] {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
					valid = false
				}
			}
			if valid {
				return nil
			}
		}
	}
	return fmt.Errorf("expected an opaque #RRGGBB color")
}
