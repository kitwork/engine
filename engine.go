package engine

import (
	"context"
	"errors"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

var (
	once         sync.Once
	compilerPool = sync.Pool{
		New: func() any {
			return compiler.NewCompiler()
		},
	}
)

// Engine = Forge + Compiler + Cache
// Không chạy Work, chỉ đúc và quản lý Blueprint
type Engine struct {
	stdlib *compiler.Environment
	cache  *lru.Cache[string, *Work] // Cache Blueprint đã đúc
}

// New khởi tạo Engine
func New() *Engine {
	cache, _ := lru.New[string, *Work](2048)

	e := &Engine{
		stdlib: compiler.NewEnvironment(),
		cache:  cache,
	}

	e.registerBuiltins()
	return e
}

// registerBuiltins đăng ký DSL gốc cho JS
func (e *Engine) registerBuiltins() {
	// work({ name: "OrderSystem" })
	fn := value.NewFunc(func(args ...value.Value) value.Value {
		name := "unnamed"
		if len(args) > 0 {
			arg := args[0]
			if arg.IsString() {
				name = arg.Text()
			} else if arg.IsMap() {
				if n := arg.Get("name"); n.IsString() {
					name = n.Text()
				}
			}
		}
		return value.New(NewWork(name))
	})

	e.stdlib.Set("work", fn)
}

// Build: Biến source JS thành một Work Blueprint hoàn chỉnh
func (e *Engine) Build(source string) (*Work, error) {
	// 1. Cache hit
	if w, ok := e.cache.Get(source); ok {
		return w, nil
	}

	// 2. Parse JS -> AST
	lexer := compiler.NewLexer(source)
	parser := compiler.NewParser(lexer)
	program := parser.ParseProgram()

	if len(parser.Errors()) > 0 {
		return nil, errors.New(parser.Errors()[0])
	}

	// 3. Discovery phase: đúc Blueprint
	tempWork := NewWork("temp")
	rt := NewRuntime(context.Background(), tempWork, e.stdlib)

	// Ghi đè hàm work cục bộ để trỏ về tempWork trong giai đoạn Build
	buildWorkFn := value.NewFunc(func(args ...value.Value) value.Value {
		if len(args) > 0 {
			arg := args[0]
			if arg.IsString() {
				tempWork.Name = arg.Text()
			} else if arg.IsMap() {
				if n := arg.Get("name"); n.IsString() {
					tempWork.Name = n.Text()
				}
			}
		}
		return value.New(tempWork)
	})
	rt.env.Set("work", buildWorkFn)

	// 4. Discovery phase: thực thi các directive (.router, .retry...)
	compiler.Evaluator(program, rt.env)

	// 5. Gắn "linh hồn" (logic thực thi)
	tempWork.Entry(program)

	// Biên dịch AST thành Bytecode để tối ưu thực thi
	c := compilerPool.Get().(*compiler.Compiler)
	c.Reset()
	if err := c.Compile(program); err == nil {
		tempWork.Bytecode = c.ByteCodeResult()
	}
	compilerPool.Put(c)

	// 6. Cache Blueprint
	e.cache.Add(source, tempWork)

	return tempWork, nil
}

// Trigger: Kích hoạt một Work (HTTP, Cron, Queue…)
func (e *Engine) Trigger(ctx context.Context, w *Work) value.Value {
	rt := NewRuntime(ctx, w, e.stdlib)
	res := rt.Execute()

	// TỰ ĐỘNG: Nếu script kết thúc mà w.Response vẫn chưa được set chủ động,
	// ta lấy giá trị trả về cuối cùng làm Response mặc định.
	if w.Response.IsNil() || w.Response.IsInvalid() {
		if !res.IsNil() && !res.IsInvalid() {
			// Nếu res là DBQuery hoặc struct có phương thức Get(), ta thực thi để lấy dữ liệu
			if q, ok := res.V.(*DBQuery); ok {
				w.Response = q.Get()
			} else {
				w.Response = res
			}
		}
	}

	return res
}
