package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/value"
)

type Router struct {
	Method string
	Path   string
}

type Work struct {
	IDStr   string `json:"id"`
	Name    string `json:"name"`
	KindStr string `json:"kind"`
	Ver     string `json:"version"`

	Routes     []*Router     `json:"routes"`
	Retries    int           `json:"retries"`
	TimeoutDur time.Duration `json:"timeout"`

	// EntryBlock giữ AST hoặc Bytecode
	EntryBlock interface{}        `json:"-"`
	Bytecode   *compiler.Bytecode `json:"-"`

	// Runtime context data
	Response value.Value `json:"-"`
}

func NewWork(name string) *Work {
	return &Work{Name: name, Response: value.NewNull()}
}

// DSL / Context Methods

func (w *Work) JSON(val value.Value) *Work {
	w.Response = val
	return w
}

func (w *Work) Now() value.Value {
	return value.New(time.Now())
}

func (w *Work) Router(method, path string) *Work {
	w.Routes = append(w.Routes, &Router{Method: strings.ToUpper(method), Path: path})
	return w
}

func (w *Work) Retry(times int, delay string) *Work {
	w.Retries = times
	// Có thể thêm Logic xử lý delay ở đây nếu cần lưu riêng
	return w
}

func (w *Work) Timeout(duration string) *Work {
	if d, err := time.ParseDuration(duration); err == nil {
		w.TimeoutDur = d
	}
	return w
}

func (w *Work) Version(v string) *Work {
	w.Ver = v
	return w
}

func (w *Work) Daily(timeStr string) *Work {
	// Logic đánh dấu đây là một Cron job chạy hàng ngày
	w.KindStr = "cron"
	return w
}

func (w *Work) Entry(block interface{}) *Work {
	w.EntryBlock = block
	return w
}

func (w *Work) Kind() string {
	if w.KindStr != "" {
		return w.KindStr
	}
	w.KindStr = "task"
	if len(w.Routes) > 0 {
		w.KindStr = "router"
	}
	return w.KindStr
}

func (w *Work) ID() string {
	if w.IDStr != "" {
		return w.IDStr
	}
	if w.Kind() == "router" {
		var parts []string
		for _, r := range w.Routes {
			parts = append(parts, r.Method+r.Path)
		}
		w.IDStr = "RT:" + strings.Join(parts, "|")
	} else {
		w.IDStr = "WK:" + w.Name
	}
	return w.IDStr
}

// Helpers cho Script
func (w *Work) Print(args ...value.Value) {
	for _, arg := range args {
		fmt.Print(arg.Text(), " ")
	}
	fmt.Println()
}
