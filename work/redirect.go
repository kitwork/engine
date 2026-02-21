package work

import (
	"fmt"
	"net/http"
)

type Redirect struct {
	URL  string
	Code int
}

func (w *Work) Redirect(url string, code ...int) *Work {
	if len(w.CoreRouter.Routes) > 0 {
		lastRoute := w.CoreRouter.Routes[len(w.CoreRouter.Routes)-1]
		lastRoute.Redirect = &Redirect{
			URL:  url,
			Code: http.StatusFound,
		}
		if len(code) > 0 {
			lastRoute.Redirect.Code = code[0]
			fmt.Printf("Redirect Code: %d\n", code[0])
		}
	}
	return w
}
