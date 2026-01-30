package work

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Regex mới: Hỗ trợ khoảng trắng và ký tự $ (ví dụ {{ $navbar }} hoặc {{user.name}})
var placeholderRegex = regexp.MustCompile(`\{\{\s*([$a-zA-Z0-9._]+)\s*\}\}`)

// Render là bộ máy hiển thị công nghiệp của Kitwork.
// Quy ước:
// - {{ $variable }} -> Raw HTML (Layouts, Components)
// - {{ variable }}  -> Escaped String (User Data - XSS Safe)
func Render(tmpl string, data map[string]any) string {
	if tmpl == "" {
		return ""
	}

	return placeholderRegex.ReplaceAllStringFunc(tmpl, func(match string) string {
		// 1. Lấy key và trim khoảng trắng
		rawKey := strings.Trim(match, "{} ")

		// 2. Xác định chế độ Raw dựa trên tiền tố $
		isRaw := strings.HasPrefix(rawKey, "$")

		// 3. Chuẩn hóa key (bỏ $ nếu có) để lookup data
		key := rawKey
		if isRaw {
			key = strings.TrimPrefix(rawKey, "$")
		}

		// 4. Truy xuất giá trị
		val := resolvePath(key, data)
		if val == nil {
			return ""
		}
		strVal := fmt.Sprintf("%v", val)

		// 5. Trả về kết quả tùy theo chế độ
		if isRaw {
			return strVal // Raw HTML
		}
		return html.EscapeString(strVal) // XSS Protection
	})
}

// resolvePath hỗ trợ truy xuất sâu vào map (ví dụ: "user.profile.name")
func resolvePath(path string, data map[string]any) any {
	parts := strings.Split(path, ".")
	var current any = data
	for _, part := range parts {
		if m, ok := current.(map[string]any); ok {
			current = m[part]
		} else {
			return nil
		}
	}
	return current
}
