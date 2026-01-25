package character

// Các hằng số định nghĩa loại ký tự (Byte-sized cho bộ nhớ tối ưu)
const (
	Other    uint8 = iota // Ký tự lạ, không hợp lệ
	Space                 // ' ', \t, \n, \r
	Alpha                 // a-z, A-Z, _, $
	Digit                 // 0-9
	Operator              // + - * / % = ! < > & | . ? : , ; ( ) [ ] { }
	Quote                 // " '
)

// Table tra cứu nhanh cho 256 ký tự ASCII.
// Việc dùng mảng cố định giúp CPU truy cập O(1) cực nhanh.
var Table = [256]uint8{}

func init() {
	// 1. Mặc định tất cả là Other
	for i := 0; i < 256; i++ {
		Table[i] = Other
	}

	// 2. Khoảng trắng (ASCII <= 32 bao gồm space, tab, newline)
	for i := 0; i <= 32; i++ {
		Table[i] = Space
	}

	// 3. Chữ cái (Alpha)
	for i := 'a'; i <= 'z'; i++ {
		Table[i] = Alpha
	}
	for i := 'A'; i <= 'Z'; i++ {
		Table[i] = Alpha
	}
	Table['_'] = Alpha
	Table['$'] = Alpha

	// 4. Chữ số (Digit)
	for i := '0'; i <= '9'; i++ {
		Table[i] = Digit
	}

	// 5. Toán tử & Ký hiệu (Operator)
	// Gán trực tiếp từng byte thay vì dùng strings.Contains để tránh overhead
	ops := "=+-*/%&|^.!<>?:,;()[]{}"
	for i := 0; i < len(ops); i++ {
		Table[ops[i]] = Operator
	}

	// 6. Dấu ngoặc kép/đơn/huyền (Quote)
	Table['"'] = Quote
	Table['\''] = Quote
	Table['`'] = Quote
}
