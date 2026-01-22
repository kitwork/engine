package engine

import (
	"github.com/kitwork/engine/value"
)

func init() {
	// Đăng ký các phương thức "Format & Cast" cho toàn bộ các Kind (thông qua Any)

	// .json() - Trả về chính nó, dùng để đánh dấu ý định trả về JSON
	value.Any.Prototype("json", func(target value.Value, args ...value.Value) value.Value {
		return target
	})

	// .string() - Chuyển thành chuỗi
	value.Any.Prototype("string", func(target value.Value, args ...value.Value) value.Value {
		return value.New(target.Text())
	})

	// .int() - Chuyển thành số nguyên
	value.Any.Prototype("int", func(target value.Value, args ...value.Value) value.Value {
		return value.New(target.Int())
	})

	// .html() / .text() - Trả về chính nó/định dạng
	value.Any.Prototype("html", func(target value.Value, args ...value.Value) value.Value {
		return target
	})

	value.Any.Prototype("text", func(target value.Value, args ...value.Value) value.Value {
		return value.New(target.Text())
	})
}
