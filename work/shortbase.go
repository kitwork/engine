package work

import (
	shortbase "github.com/kitwork/engine/utilities/shortbase"
	"github.com/kitwork/engine/value"
)

// Shortbase is the kitwork() binding for the reversible base-N codec — import { shortbase } from
// "kitwork". The codec itself lives in helpers/shortbase (pure Go); this only bridges its string API
// to the value.Value (JS) surface and keeps the fluent chain (from/to/prefix/suffix → encode/decode).
type Shortbase struct {
	codec *shortbase.Codec
}

func (w *KitWork) Shortbase() *Shortbase {
	return &Shortbase{codec: shortbase.Default()}
}

func (s *Shortbase) From(args ...value.Value) *Shortbase {
	if len(args) > 0 {
		return &Shortbase{codec: s.codec.From(args[0].String())}
	}
	return s
}

func (s *Shortbase) To(args ...value.Value) *Shortbase {
	if len(args) > 0 {
		return &Shortbase{codec: s.codec.To(args[0].String())}
	}
	return s
}

func (s *Shortbase) Prefix(args ...value.Value) *Shortbase {
	if len(args) > 0 {
		return &Shortbase{codec: s.codec.Prefix(args[0].String())}
	}
	return s
}

func (s *Shortbase) Suffix(args ...value.Value) *Shortbase {
	if len(args) > 0 {
		return &Shortbase{codec: s.codec.Suffix(args[0].String())}
	}
	return s
}

func (s *Shortbase) Encode(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.NewNil()
	}
	if code, ok := s.codec.Encode(args[0].String()); ok {
		return value.NewString(code)
	}
	return value.NewNil()
}

func (s *Shortbase) Decode(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.NewNil()
	}
	if val, ok := s.codec.Decode(args[0].String()); ok {
		return value.NewString(val)
	}
	return value.NewNil()
}
