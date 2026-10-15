package engine

import (
	"fmt"
	"math"

	"github.com/kitwork/engine/value"
)

func validateDesktopPosition(option value.Value) error {
	if option.K == value.String {
		switch option.Text() {
		case "center", "top-left", "top-right", "bottom-left", "bottom-right":
			return nil
		}
	}
	if option.IsMap() && len(option.Map()) == 2 {
		for _, key := range []string{"x", "y"} {
			v := option.Map()[key]
			if v.K != value.Number || math.IsNaN(v.N) || math.IsInf(v.N, 0) || v.N < 0 || v.N > 100000 || v.N != math.Trunc(v.N) {
				return fmt.Errorf("%s must be an integer from 0 to 100000", key)
			}
		}
		return nil
	}
	return fmt.Errorf("expected center, a corner name, or { x, y }")
}
