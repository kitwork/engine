package work

import "github.com/kitwork/engine/value"

type Response struct {
	data    value.Value
	typing  string
	code    int
	headers map[string]string
}
