package engine

import (
	"context"
	"fmt"

	"github.com/kitwork/engine/core"
	"github.com/kitwork/engine/value"
)

// Script thực thi một đoạn mã script và trả về kết quả chi tiết
func Script(source string) *Result {
	e := core.New()
	w, err := e.Build(source, "", "", "")
	if err != nil {
		return &Result{errors: err}
	}

	res := e.Trigger(context.Background(), w, nil, nil)
	if res.Error != "" {
		return &Result{errors: fmt.Errorf("%s", res.Error)}
	}

	// Ưu tiên trả về Response (từ json/html) nếu có, nếu không trả về Value cuối cùng
	val := res.Response.Data
	if val.K == value.Nil {
		val = res.Value
	}
	return &Result{
		value:  val,
		energy: res.Energy,
	}
}
