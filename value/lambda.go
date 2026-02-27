package value

type Lambda struct {
	Address int
	Params  []string
	Scope   map[string]Value
}
