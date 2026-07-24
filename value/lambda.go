package value

type Lambda struct {
	Address int
	Params  []string
	Scope   map[string]Value

	// Program identifies the immutable bytecode that owns Address. A detached
	// closure cannot safely execute from an address alone.
	Code      []byte
	Constants []Value
	SourceMap []int32

	// Parent là closure bao ngoài (nếu có) — tạo thành chuỗi scope (scope chain)
	// để lambda lồng nhiều cấp vẫn đọc/ghi được biến của các hàm bao ngoài,
	// đúng ngữ nghĩa lexical scoping của JavaScript.
	Parent *Lambda
}
