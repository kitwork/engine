package work

import (
	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/value"
)

// palette() and pigment() let a site state its identity once — one hue — instead of hand-typing
// eight hexes that drift apart as a design evolves:
//
//	import { router, palette, pigment } from "kitwork";
//	colors: {
//	  brand: palette("#635bff"),                 // DEFAULT · deep · soft · wash
//	  ink:   pigment("#635bff"),                 // brand-tinted text ramp, AA-guarded
//	  brand: { ...palette("#635bff"), deep: "#4320b3" },   // any rung stays overridable
//	}
//
// Both return a plain object of hexes, so the existing theme parser flattens them exactly like a
// hand-written one (DEFAULT loses its suffix) and nothing about `colors` changes.

// hexMap turns a derived name→hex map into the object a stylesheet config expects. A nil map
// (an unparseable hex) becomes nil so the mistake surfaces as a missing colour rather than a
// silently wrong one.
func hexMap(m map[string]string) value.Value {
	if m == nil {
		return value.Value{K: value.Nil}
	}
	out := make(map[string]value.Value, len(m))
	for name, hex := range m {
		out[name] = value.NewString(hex)
	}
	return value.Value{K: value.Map, V: out}
}

// Palette is what `import { palette } from "kitwork"` resolves to (0-arg getter, auto-called):
// a function growing an accent family — brand, or any status colour — from a single hue.
func (w *KitWork) Palette() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 || !args[0].IsString() {
			return value.Value{K: value.Nil}
		}
		return hexMap(jitcss.Palette(args[0].String()))
	})
}

// Pigment is what `import { pigment } from "kitwork"` resolves to: the text ramp derived from the
// same hue, its body rungs contrast-guarded so they clear WCAG AA on the canvas.
//
// The canvas cannot be inferred once surfaces are written by hand, so it is passed explicitly —
// pigment("#635bff", { on: "#f6f9fc" }) — and defaults to the house canvas when omitted.
func (w *KitWork) Pigment() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 || !args[0].IsString() {
			return value.Value{K: value.Nil}
		}
		canvas := ""
		if len(args) > 1 {
			switch {
			case args[1].IsString():
				canvas = args[1].String()
			case args[1].K == value.Map:
				if on, ok := args[1].Map()["on"]; ok && on.IsString() {
					canvas = on.String()
				}
			}
		}
		return hexMap(jitcss.Pigment(args[0].String(), canvas))
	})
}
