package core

import (
	"fmt"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

func Script(script string, args ...value.Value) (value.Value, error) {
	stdlib := compiler.NewEnvironment()

	// Inject bất kỳ argument nào gửi vào Script vào biến môi trường nếu cần thiết
	// Ở mức độ này, hàm Script đơn giản chỉ khởi tạo và chạy
	l := compiler.NewLexer(script)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return value.Value{K: value.Invalid}, fmt.Errorf("compile error: %s", p.Errors()[0])
	}

	return compiler.Evaluator(prog, stdlib), nil
}
