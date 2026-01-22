package engine

import (
	"github.com/kitwork/engine/value"
)

func init() {
	// Đăng ký các phương thức "Format & Cast" cho toàn bộ các Kind (thông qua Any)

	// .json() - Đánh dấu hoặc trả về chính nó để chaining
	value.Any.Prototype("json", func(target value.Value, args ...value.Value) value.Value {
		// Ở đây có thể thêm logic để set Content-Type nếu cần
		return target
	})

	// .string() - Ép kiểu sang chuỗi
	value.Any.Prototype("string", func(target value.Value, args ...value.Value) value.Value {
		return value.New(target.Text())
	})

	// .int() - Ép kiểu sang số nguyên
	value.Any.Prototype("int", func(target value.Value, args ...value.Value) value.Value {
		return value.New(target.Int())
	})

	// .float() - Ép kiểu sang số thực
	value.Any.Prototype("float", func(target value.Value, args ...value.Value) value.Value {
		return value.New(target.Float())
	})

	// .html() / .text() - Tương lai sẽ dùng để định dạng response
	value.Any.Prototype("html", func(target value.Value, args ...value.Value) value.Value {
		return target
	})

	value.Any.Prototype("text", func(target value.Value, args ...value.Value) value.Value {
		return value.New(target.Text())
	})
}
