package work

import (
	"fmt"
	"time"

	"github.com/kitwork/engine/value"
)

// Task đại diện cho một phiên thực thi (Mutable Context)
type Task struct {
	Work *Work

	Response Response
	Error    string
	Config   map[string]string
}

func (t *Task) Reset(w *Work) {
	t.Work = w
	t.Response.Data = value.Value{K: value.Nil}
	t.Response.Type = ""
	t.Response.Code = 0
	if t.Response.Headers != nil {
		for k := range t.Response.Headers {
			delete(t.Response.Headers, k)
		}
	}
	t.Error = ""

	if t.Config == nil {
		t.Config = make(map[string]string)
	} else {
		for k := range t.Config {
			delete(t.Config, k)
		}
	}
}

// Removed SetRequest

func (t *Task) JSON(val value.Value) {
	t.Response.Data = val
	t.Response.Type = "json"
}

func (t *Task) HTML(template value.Value, data ...value.Value) {
	if len(data) > 0 {
		res := make(map[string]value.Value)
		res["template"] = template
		res["data"] = data[0]
		t.Response.Data = value.New(res)
	} else {
		t.Response.Data = template
	}
	t.Response.Type = "html"
}

func (t *Task) Status(code int) {
	t.Response.Code = code
}

func (t *Task) Header(key, val string) {
	if t.Response.Headers == nil {
		t.Response.Headers = make(map[string]string)
	}
	t.Response.Headers[key] = val
}

func (t *Task) Now() value.Value { return value.New(time.Now()) }
func (t *Task) DB(conn ...string) *DBQuery {
	q := NewDBQuery()
	if len(conn) > 0 {
		q.connection = conn[0]
	}
	return q
}
func (t *Task) Fetch() *Fetch { return NewFetch(t) }
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
		t.Response.Data = args[0]
	}
}

func (t *Task) Fail(err value.Value) {
	t.Error = err.Text()
}
