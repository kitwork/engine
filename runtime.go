package engine

import (
	"context"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/work"
)

// Runtime đại diện cho một phiên thực thi đơn lẻ (Contextual execution)
type Runtime struct {
	ctx   context.Context
	env   *compiler.Environment
	work  *work.Work
	start time.Time
}

// NewRuntime tạo Runtime mới (Dùng cho các trường hợp đặc biệt hoặc Builder)
// Trong vận hành bình thường, Engine sẽ dùng pool và Trigger()
func NewRuntime(ctx context.Context, w *work.Work, global *compiler.Environment) *Runtime {
	env := compiler.NewEnvironment()
	env.SetOuter(global)

	return &Runtime{
		ctx:   ctx,
		env:   env,
		work:  w,
		start: time.Now(),
	}
}

func (rt *Runtime) Execute(nodes ...compiler.Node) {
	// Hiện tại Engine.Trigger đã xử lý thực thi bytecode tối ưu.
	// Hàm này có thể giữ lại cho các nhu cầu mở rộng hoặc chạy AST trực tiếp.
}
