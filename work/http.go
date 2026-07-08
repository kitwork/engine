package work

import (
	"github.com/kitwork/engine/helpers/http"
)

var AllowLocal bool
var ServerPort int

type HTTP = http.HTTP
type HTTPResponse = http.Response

func (w *KitWork) HTTP() *HTTP {
	return &HTTP{}
}
