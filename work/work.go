package work

import (
	"fmt"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

// StaticRoute đại diện cho một rule điều hướng tĩnh
type StaticRoute struct {
	Method  string
	Path    string
	Handler *value.ScriptFunction
}

// Work là Blueprint (Bản thiết kế) - IMMUTABLE
// Đối tượng này được tạo ra lúc Build và dùng chung cho mọi Request.
// Tuyệt đối không lưu dữ liệu thay đổi (Response, Variables) ở đây.
type Work struct {
	Name       string
	Routes     []*StaticRoute
	Retries    int
	TimeoutDur time.Duration
	Ver        string

	Bytecode *compiler.Bytecode
}

func NewWork(name string) *Work {
	return &Work{Name: name}
}

// Task là Execution State (Trạng thái thực thi) - MUTABLE
// Mỗi Request sẽ có một Task riêng. Được quản lý bởi sync.Pool để tối ưu.
type Task struct {
	Work     *Work                  // Trỏ về bản thiết kế
	Response value.Value            // Kết quả trả về của riêng Request này
	ResType  string                 // "json", "html", etc.
	Params   map[string]value.Value // Tham số đầu vào (payload, query params)
	Config   map[string]string      // Cấu hình hệ thống (env vars)
	Data     interface{}            // Slot mở rộng dữ liệu runtime
}

func (t *Task) Reset(w *Work) {
	t.Work = w
	t.Response = value.Value{K: value.Nil}
	t.ResType = "json"

	// Reset maps to avoid memory leaks and reuse capacity
	if t.Params != nil {
		for k := range t.Params {
			delete(t.Params, k)
		}
	} else {
		t.Params = make(map[string]value.Value)
	}

	if t.Config != nil {
		for k := range t.Config {
			delete(t.Config, k)
		}
	} else {
		t.Config = make(map[string]string)
	}
}

// Helpers cho Script (Bây giờ nhận Task làm receiver)

func (t *Task) JSON(val value.Value) {
	t.Response = val
	t.ResType = "json"
}

func (t *Task) HTML(template value.Value, data ...value.Value) {
	if len(data) > 0 {
		res := make(map[string]value.Value)
		res["template"] = template
		res["data"] = data[0]
		t.Response = value.New(res)
	} else {
		t.Response = template
	}
	t.ResType = "html"
}

func (t *Task) Now() value.Value {
	return value.New(time.Now())
}

func (t *Task) DB() *DBQuery {
	return NewDBQuery()
}

func (t *Task) HTTP() *HTTPClient {
	return NewHTTPClient(t)
}

func (t *Task) Payload() value.Value {
	return value.New(t.Params)
}

func (t *Task) Log(args ...value.Value) {
	fmt.Printf("[%s] [%s] ", time.Now().Format("15:04:05"), t.Work.Name)
	for _, arg := range args {
		fmt.Print(arg.Text(), " ")
	}
	fmt.Println()
}

// Discovery methods (Chỉ dùng lúc Build - vẫn nằm trên Work)
func (w *Work) Router(method, path string) *Work {
	w.Routes = append(w.Routes, &StaticRoute{Method: strings.ToUpper(method), Path: path})
	return w
}

func (w *Work) Handle(fn value.Value) *Work {
	if len(w.Routes) > 0 {
		lastRoute := w.Routes[len(w.Routes)-1]
		if sFn, ok := fn.V.(*value.ScriptFunction); ok {
			lastRoute.Handler = sFn
		}
	}
	return w
}

func (w *Work) Retry(times int, _ string) *Work {

	w.Retries = times
	return w
}

func (w *Work) Version(v string) *Work {
	w.Ver = v
	return w
}

func (w *Work) Entry(_ interface{}) *Work {
	// Giữ lại để tương thích, Bytecode đã được lưu riêng
	return w
}

func (t *Task) Print(args ...value.Value) {
	for _, arg := range args {
		fmt.Print(arg.Text(), " ")
	}
	fmt.Println()
}
