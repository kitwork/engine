package value

type Script struct {
	Address    int
	ParamNames []string
	Scope      map[string]Value
}
