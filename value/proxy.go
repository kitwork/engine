package value

// ProxyHandler cho phép các module khác nhau định nghĩa cách xử lý logic biểu tượng
type ProxyHandler interface {
	OnGet(key string) Value
	OnCompare(op string, other Value) Value
	OnInvoke(method string, args ...Value) Value
}
