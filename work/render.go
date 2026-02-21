package work

import (
	"time"

	"github.com/kitwork/engine/value"
)

type Template struct {
	Page   string
	Layout map[string]string
}

type RenderCore struct {
	CacheDuration  time.Duration
	StaticDuration time.Duration
	StaticCheck    bool
	StaticDir      string
	StaticPrefix   string
	ResourcePath   string
}

func (w *Work) Render(arg any) *Work {
	if len(w.CoreRouter.Routes) > 0 {
		last := w.CoreRouter.Routes[len(w.CoreRouter.Routes)-1]
		if last.Template == nil {
			last.Template = &Template{}
		}

		var v value.Value
		switch val := arg.(type) {
		case string:
			last.Template.Page = val
			return w
		case value.Value:
			v = val
		case *value.Value:
			v = *val
		default:
			return w
		}

		if v.IsString() {
			last.Template.Page = v.Text()
		} else if v.IsMap() {
			m := v.Map()
			if main, ok := m["main"]; ok {
				last.Template.Page = main.Text()
			}
			if last.Template.Layout == nil {
				last.Template.Layout = make(map[string]string)
			}
			for k, val := range m {
				if k != "main" {
					last.Template.Layout[k] = val.Text()
				}
			}
		}
	}
	return w
}

func (w *Work) Layout(arg value.Value) *Work {
	if len(w.CoreRouter.Routes) > 0 && arg.IsMap() {
		last := w.CoreRouter.Routes[len(w.CoreRouter.Routes)-1]
		if last.Template == nil {
			last.Template = &Template{}
		}
		if last.Template.Layout == nil {
			last.Template.Layout = make(map[string]string)
		}
		m := arg.Map()
		for k, v := range m {
			last.Template.Layout[k] = v.Text()
		}
	}
	return w
}

func (w *Work) Static(args ...any) *Work {
	if len(args) == 0 {
		return w
	}
	arg := args[0]
	switch v := arg.(type) {
	case string, float64, int:
		w.CoreRender.StaticDuration = parseDuration(v)
	case value.Value:
		if v.IsMap() {
			m := v.Map()
			if d, ok := m["duration"]; ok {
				w.CoreRender.StaticDuration = parseDuration(d)
			}
			if c, ok := m["check"]; ok {
				w.CoreRender.StaticCheck = c.IsTrue()
			}
		} else {
			w.CoreRender.StaticDuration = parseDuration(v)
		}
	}
	return w
}

func (w *Work) File(path string) *Work {
	w.CoreRender.ResourcePath = path
	return w
}

func (w *Work) Assets(path string) *Work {
	w.CoreRender.ResourcePath = path
	return w
}
