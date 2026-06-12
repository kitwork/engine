package value

type Lambda struct {
	Address int
	Params  []string
	Scope   map[string]Value

	// Parent là closure bao ngoài (nếu có) — tạo thành chuỗi scope (scope chain)
	// để lambda lồng nhiều cấp vẫn đọc/ghi được biến của các hàm bao ngoài,
	// đúng ngữ nghĩa lexical scoping của JavaScript.
	Parent *Lambda
}
