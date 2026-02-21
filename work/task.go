package work

import (
	"fmt"
	"net/http"
	"time"

	"github.com/kitwork/engine/value"
)

// Task đại diện cho một phiên thực thi (Mutable Context)
type Task struct {
	Work    *Work
	Request *http.Request
	Writer  http.ResponseWriter

	Params map[string]value.Value // URL Path params like :id

	Response value.Value
	ResType  string
	Error    string
	Config   map[string]string
}

func (t *Task) Reset(w *Work) {
	t.Work = w
	t.Request = nil
	t.Writer = nil
	t.Response = value.Value{K: value.Nil}
	t.ResType = ""
	t.Error = ""

	if t.Params == nil {
		t.Params = make(map[string]value.Value)
	} else {
		for k := range t.Params {
			delete(t.Params, k)
		}
	}

	if t.Config == nil {
		t.Config = make(map[string]string)
	} else {
		for k := range t.Config {
			delete(t.Config, k)
		}
	}
}

func (t *Task) SetRequest(r *http.Request, w http.ResponseWriter) {
	t.Request = r
	t.Writer = w
}

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

func (t *Task) Now() value.Value { return value.New(time.Now()) }
func (t *Task) DB(conn ...string) *DBQuery {
	q := NewDBQuery()
	if len(conn) > 0 {
		q.connection = conn[0]
	}
	return q
}
func (t *Task) HTTP() *HTTPClient { return NewHTTPClient(t) }

func (t *Task) GetQuery() value.Value  { return value.NewNull() }
func (t *Task) SetQuery(v value.Value) {}
func (t *Task) GetBody() value.Value   { return value.NewNull() }
func (t *Task) SetBody(v value.Value)  {}
func (t *Task) GetParams() value.Value { return value.New(t.Params) }

// Shared empty map to avoid allocation
var zeroPayload = value.New(map[string]value.Value{})

func (t *Task) Payload() value.Value {
	if len(t.Params) == 0 {
		return zeroPayload
	}
	res := make(map[string]value.Value, len(t.Params))
	for k, v := range t.Params {
		res[k] = v
	}
	return value.New(res)
}
func (t *Task) Log(args ...value.Value) {
	fmt.Printf("[%s] [%s] ", time.Now().Format("15:04:05"), t.Work.Name)
	for _, arg := range args {
		fmt.Print(arg.Text(), " ")
	}
	fmt.Println()
}
func (t *Task) Print(args ...value.Value) {
	for _, arg := range args {
		fmt.Print(arg.Text(), " ")
	}
	fmt.Println()
}

func (t *Task) Done(args ...value.Value) {
	if len(args) > 0 {
		t.Response = args[0]
	}
}

func (t *Task) Fail(err value.Value) {
	t.Error = err.Text()
}
