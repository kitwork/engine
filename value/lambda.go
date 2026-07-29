package value

// ProgramRef is an opaque handle to the immutable program that owns a lambda.
// The value package deliberately exposes no bytecode storage through this seam.
type ProgramRef interface {
	ProgramVersion() uint16
}

type Lambda struct {
	Address      int
	Name         string
	SourceFile   string
	SourceLine   int32
	SourceColumn int32
	Params       []string
	Scope        map[string]Value

	// Program identifies the immutable bytecode that owns Address. A detached
	// closure cannot safely execute from an address alone.
	Program ProgramRef

	// Parent là closure bao ngoài (nếu có) — tạo thành chuỗi scope (scope chain)
	// để lambda lồng nhiều cấp vẫn đọc/ghi được biến của các hàm bao ngoài,
	// đúng ngữ nghĩa lexical scoping của JavaScript.
	Parent *Lambda
}
