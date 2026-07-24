package shortbase

import (
	"github.com/kitwork/engine/capabilities"
	sb "github.com/kitwork/engine/utilities/shortbase"
	"github.com/kitwork/engine/value"
)

type ShortbaseAdapter struct {
	scope capabilities.Scope
	codec *sb.Codec
}

func NewShortbaseAdapter(scope capabilities.Scope) *ShortbaseAdapter {
	return &ShortbaseAdapter{
		scope: scope,
		codec: sb.Default(),
	}
}

func (s *ShortbaseAdapter) From(args ...value.Value) *ShortbaseAdapter {
	if len(args) > 0 {
		return &ShortbaseAdapter{scope: s.scope, codec: s.codec.From(args[0].Text())}
	}
	return s
}

func (s *ShortbaseAdapter) To(args ...value.Value) *ShortbaseAdapter {
	if len(args) > 0 {
		return &ShortbaseAdapter{scope: s.scope, codec: s.codec.To(args[0].Text())}
	}
	return s
}

func (s *ShortbaseAdapter) Prefix(args ...value.Value) *ShortbaseAdapter {
	if len(args) > 0 {
		return &ShortbaseAdapter{scope: s.scope, codec: s.codec.Prefix(args[0].Text())}
	}
	return s
}

func (s *ShortbaseAdapter) Suffix(args ...value.Value) *ShortbaseAdapter {
	if len(args) > 0 {
		return &ShortbaseAdapter{scope: s.scope, codec: s.codec.Suffix(args[0].Text())}
	}
	return s
}

func (s *ShortbaseAdapter) Encode(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.NewNil()
	}
	if code, ok := s.codec.Encode(args[0].Text()); ok {
		return value.NewString(code)
	}
	return value.NewNil()
}

func (s *ShortbaseAdapter) Decode(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.NewNil()
	}
	if val, ok := s.codec.Decode(args[0].Text()); ok {
		return value.NewString(val)
	}
	return value.NewNil()
}

func init() {
	capabilities.DefaultRegistry.Register("shortbase", func(scope capabilities.Scope) value.Value {
		return value.New(NewShortbaseAdapter(scope))
	})
}
