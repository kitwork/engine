package builtins

import (
	"math"
	"strconv"
	"strings"

	"github.com/kitwork/engine/value"
)

func Number() value.Value {
	toNumber := func(v value.Value) value.Value {
		switch v.K {
		case value.Number:
			return v
		case value.Bool:
			return value.Value{K: value.Number, N: v.N}
		case value.String:
			s := strings.TrimSpace(v.Text())
			if s == "" {
				return value.Value{K: value.Number, N: 0}
			}
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return value.New(f)
			}
			return value.Value{K: value.Nil}
		case value.Nil:
			return value.Value{K: value.Number, N: 0}
		}
		return value.Value{K: value.Nil}
	}

	ctor := func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.Value{K: value.Number, N: 0}
		}
		return toNumber(args[0])
	}

	props := map[string]value.Value{
		"MAX_SAFE_INTEGER": value.New(9007199254740991.0),
		"MIN_SAFE_INTEGER": value.New(-9007199254740991.0),
		"EPSILON":          value.New(2.220446049250313e-16),

		"isInteger": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Number {
				return value.FALSE
			}
			return value.ToBool(args[0].N == float64(int64(args[0].N)))
		}),
		"isFinite": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 || args[0].K != value.Number {
				return value.FALSE
			}
			return value.ToBool(!math.IsInf(args[0].N, 0) && !math.IsNaN(args[0].N))
		}),
		"parseFloat": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.Value{K: value.Nil}
			}
			return toNumber(args[0])
		}),
		"parseInt": value.NewFunc(func(args ...value.Value) value.Value {
			if len(args) == 0 {
				return value.Value{K: value.Nil}
			}
			n := toNumber(args[0])
			if n.K != value.Number {
				return n
			}
			return value.Value{K: value.Number, N: float64(int64(n.N))}
		}),
	}

	return value.NewFuncObject(ctor, props)
}

func ParseFloat() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.New(0.0)
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(args[0].Text()), 64)
		if err != nil {
			return value.New(0.0)
		}
		return value.New(f)
	})
}

func ParseInt() value.Value {
	return value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) == 0 {
			return value.New(0)
		}
		s := args[0].Text()
		base := 10
		if len(args) > 1 && args[1].K == value.Number {
			base = int(args[1].N)
		}
		if base < 2 || base > 36 {
			base = 10
		}
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			return value.New(0)
		}
		if base == 16 && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
			s = s[2:]
		}
		i, err := strconv.ParseInt(s, base, 64)
		if err == nil {
			return value.New(i)
		}
		var prefix strings.Builder
		for idx, ch := range s {
			if idx == 0 && (ch == '+' || ch == '-') {
				prefix.WriteRune(ch)
				continue
			}
			isValid := false
			if ch >= '0' && ch <= '9' {
				isValid = int(ch-'0') < base
			} else if ch >= 'a' && ch <= 'z' {
				isValid = int(ch-'a'+10) < base
			} else if ch >= 'A' && ch <= 'Z' {
				isValid = int(ch-'A'+10) < base
			}
			if !isValid {
				break
			}
			prefix.WriteRune(ch)
		}
		parsedStr := prefix.String()
		if parsedStr == "" || parsedStr == "+" || parsedStr == "-" {
			return value.New(0)
		}
		i, err = strconv.ParseInt(parsedStr, base, 64)
		if err != nil {
			return value.New(0)
		}
		return value.New(i)
	})
}
