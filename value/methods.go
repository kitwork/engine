package value

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

/* =============================================================================
   STANDARD VALUE METHODS (JS-Like Standard Library)
   ============================================================================= */

// --- Casting & Conversion ---

func (v Value) ToString(args ...Value) Value {
	if len(args) > 0 {
		format := args[0].Text()

		// Date Formatting
		if v.K == Time {
			layout := convertStrftime(format)
			t := time.Unix(0, int64(v.N))
			return NewString(t.Format(layout))
		}

		// Numeric Formatting (Pattern vs Printf)
		if v.IsNumeric() {
			if strings.Contains(format, "%") {
				return NewString(fmt.Sprintf(format, v.Interface()))
			}
			// Use C# Style Pattern (e.g. "#,##0.00")
			return NewString(formatNumericPattern(v.Float(), format))
		}

		// Generic Formatting
		if strings.Contains(format, "%") {
			return NewString(fmt.Sprintf(format, v.Interface()))
		}
	}
	v.V = v.Text()
	v.K = String
	return v
}
func (v Value) Integer(_ ...Value) Value { v.N = float64(int64(v.N)); v.K = Number; return v }
func (v Value) ToFloat(_ ...Value) Value { v.K = Number; return v }
func (v Value) Length(_ ...Value) Value {
	// String đếm theo rune — đồng bộ với thuộc tính .length
	if v.K == String {
		if s, ok := v.V.(string); ok {
			return Value{K: Number, N: float64(utf8.RuneCountInString(s))}
		}
	}
	v.N = float64(v.Len())
	v.K = Number
	return v
}

func (v Value) ToJson(args ...Value) Value {
	if len(args) > 0 {
		if args[0].V == "string" {
			v.V = string(v.ToJSON())
			v.K = String
			return v
		}
	} else if v.K == String {
		// If no args and it's a string, try to PARSE it
		var data any
		if err := json.Unmarshal([]byte(v.String()), &data); err == nil {
			return New(data)
		}
	}
	return v
}

// --- String Methods ---

func (v Value) Upper(_ ...Value) Value {
	return New(strings.ToUpper(v.Text()))
}

func (v Value) Lower(_ ...Value) Value {
	return New(strings.ToLower(v.Text()))
}

func (v Value) Capitalize(_ ...Value) Value {
	s := v.String()
	if len(s) == 0 {
		return v
	}
	// Note: Strings in Go are UTF-8, but for simple capitalize, this works.
	return New(strings.ToUpper(s[:1]) + s[1:])
}

func (v Value) Trim(_ ...Value) Value {
	return New(strings.TrimSpace(v.Text()))
}

func (v Value) Includes(args ...Value) Value {
	if len(args) == 0 {
		return FALSE
	}
	return ToBool(strings.Contains(v.String(), args[0].String()))
}

func (v Value) StartsWith(args ...Value) Value {
	if len(args) == 0 {
		return FALSE
	}
	return ToBool(strings.HasPrefix(v.String(), args[0].String()))
}

func (v Value) EndsWith(args ...Value) Value {
	if len(args) == 0 {
		return FALSE
	}
	return ToBool(strings.HasSuffix(v.String(), args[0].String()))
}

func (v Value) Split(args ...Value) Value {
	s := v.String()
	// Chuẩn JS: split() không đối số trả về mảng chứa nguyên chuỗi
	if len(args) == 0 {
		return New([]Value{{K: String, V: s}})
	}
	sep := args[0].String()
	parts := strings.Split(s, sep)
	res := make([]Value, len(parts))
	for i, p := range parts {
		res[i] = Value{K: String, V: p}
	}
	return New(res)
}

// Replace thay thế LẦN XUẤT HIỆN ĐẦU TIÊN — đúng chuẩn JS String.replace.
// Muốn thay tất cả, dùng replaceAll.
func (v Value) Replace(args ...Value) Value {
	if len(args) < 2 {
		return v
	}
	v.V = strings.Replace(v.String(), args[0].String(), args[1].String(), 1)
	return v
}

// ReplaceAll thay thế tất cả — chuẩn JS String.replaceAll.
func (v Value) ReplaceAll(args ...Value) Value {
	if len(args) < 2 {
		return v
	}
	v.V = strings.ReplaceAll(v.String(), args[0].String(), args[1].String())
	return v
}

/* ---------------------------------------------------------------------------
   Các method String chuẩn JS bổ sung.
   LƯU Ý UNICODE: mọi chỉ số (index) tính theo KÝ TỰ (rune), không phải byte —
   "Phường".slice(0, 2) trả về "Ph" và không bao giờ cắt vỡ ký tự tiếng Việt.
   (JS gốc đếm theo UTF-16 code unit; với văn bản thông thường hai cách cho
   cùng kết quả, và cách theo rune an toàn hơn cho tiếng Việt.)
   --------------------------------------------------------------------------- */

// normalizeSliceIndex chuyển index kiểu JS (cho phép âm) về [0, length].
func normalizeSliceIndex(idx int, length int) int {
	if idx < 0 {
		idx += length
	}
	if idx < 0 {
		return 0
	}
	if idx > length {
		return length
	}
	return idx
}

// Slice — chuẩn JS String.slice(start, end): hỗ trợ chỉ số âm.
func (v Value) Slice(args ...Value) Value {
	runes := []rune(v.Text())
	length := len(runes)

	start := 0
	end := length
	if len(args) > 0 && args[0].K == Number {
		start = normalizeSliceIndex(int(args[0].N), length)
	}
	if len(args) > 1 && args[1].K == Number {
		end = normalizeSliceIndex(int(args[1].N), length)
	}
	if start >= end {
		return Value{K: String, V: ""}
	}
	return Value{K: String, V: string(runes[start:end])}
}

// Substring — chuẩn JS: chỉ số âm coi như 0, start > end thì hoán đổi.
func (v Value) Substring(args ...Value) Value {
	runes := []rune(v.Text())
	length := len(runes)

	clamp := func(idx int) int {
		if idx < 0 {
			return 0
		}
		if idx > length {
			return length
		}
		return idx
	}

	start := 0
	end := length
	if len(args) > 0 && args[0].K == Number {
		start = clamp(int(args[0].N))
	}
	if len(args) > 1 && args[1].K == Number {
		end = clamp(int(args[1].N))
	}
	if start > end {
		start, end = end, start
	}
	return Value{K: String, V: string(runes[start:end])}
}

// IndexOf — trả chỉ số rune của lần xuất hiện đầu, -1 nếu không có.
// Hỗ trợ fromIndex (tính theo rune) như JS.
func (v Value) IndexOf(args ...Value) Value {
	if len(args) == 0 {
		return Value{K: Number, N: -1}
	}
	s := v.Text()
	search := args[0].Text()

	fromRune := 0
	if len(args) > 1 && args[1].K == Number {
		fromRune = normalizeSliceIndex(int(args[1].N), len([]rune(s)))
	}

	runes := []rune(s)
	offset := string(runes[fromRune:])
	byteIdx := strings.Index(offset, search)
	if byteIdx < 0 {
		return Value{K: Number, N: -1}
	}
	runeIdx := fromRune + len([]rune(offset[:byteIdx]))
	return Value{K: Number, N: float64(runeIdx)}
}

// LastIndexOf — chỉ số rune của lần xuất hiện cuối, -1 nếu không có.
func (v Value) LastIndexOf(args ...Value) Value {
	if len(args) == 0 {
		return Value{K: Number, N: -1}
	}
	s := v.Text()
	byteIdx := strings.LastIndex(s, args[0].Text())
	if byteIdx < 0 {
		return Value{K: Number, N: -1}
	}
	return Value{K: Number, N: float64(len([]rune(s[:byteIdx])))}
}

// CharAt — ký tự tại vị trí (rune index); ngoài phạm vi trả chuỗi rỗng như JS.
func (v Value) CharAt(args ...Value) Value {
	idx := 0
	if len(args) > 0 && args[0].K == Number {
		idx = int(args[0].N)
	}
	runes := []rune(v.Text())
	if idx < 0 || idx >= len(runes) {
		return Value{K: String, V: ""}
	}
	return Value{K: String, V: string(runes[idx])}
}

// CharCodeAt — code point của ký tự tại vị trí (với ký tự BMP trùng UTF-16
// code unit của JS). Ngoài phạm vi trả Nil (JS trả NaN — VM không có NaN).
func (v Value) CharCodeAt(args ...Value) Value {
	idx := 0
	if len(args) > 0 && args[0].K == Number {
		idx = int(args[0].N)
	}
	runes := []rune(v.Text())
	if idx < 0 || idx >= len(runes) {
		return Value{K: Nil}
	}
	return Value{K: Number, N: float64(runes[idx])}
}

// StrAt — chuẩn JS String.at(): hỗ trợ chỉ số âm; ngoài phạm vi trả Nil.
func (v Value) StrAt(args ...Value) Value {
	idx := 0
	if len(args) > 0 && args[0].K == Number {
		idx = int(args[0].N)
	}
	runes := []rune(v.Text())
	if idx < 0 {
		idx += len(runes)
	}
	if idx < 0 || idx >= len(runes) {
		return Value{K: Nil}
	}
	return Value{K: String, V: string(runes[idx])}
}

// maxRepeatBytes giới hạn kích thước kết quả repeat/pad để bảo vệ bộ nhớ
// trong môi trường multi-tenant (JS throw RangeError; Kitwork trả Invalid).
const maxRepeatBytes = 1 << 23 // 8 MB

// Repeat — chuẩn JS String.repeat(n).
func (v Value) Repeat(args ...Value) Value {
	count := 0
	if len(args) > 0 && args[0].K == Number {
		count = int(args[0].N)
	}
	if count <= 0 {
		return Value{K: String, V: ""}
	}
	s := v.Text()
	if len(s)*count > maxRepeatBytes {
		return Value{K: Invalid, V: "repeat: result too large (max 8MB)"}
	}
	return Value{K: String, V: strings.Repeat(s, count)}
}

// padString dựng phần đệm dài đúng `gap` rune từ chuỗi pad.
func padString(pad string, gap int) string {
	if pad == "" {
		pad = " "
	}
	padRunes := []rune(pad)
	out := make([]rune, gap)
	for i := 0; i < gap; i++ {
		out[i] = padRunes[i%len(padRunes)]
	}
	return string(out)
}

// PadStart — chuẩn JS String.padStart(targetLength, padString).
func (v Value) PadStart(args ...Value) Value {
	s := v.Text()
	target := 0
	if len(args) > 0 && args[0].K == Number {
		target = int(args[0].N)
	}
	gap := target - len([]rune(s))
	if gap <= 0 {
		return Value{K: String, V: s}
	}
	if target > maxRepeatBytes {
		return Value{K: Invalid, V: "padStart: result too large (max 8MB)"}
	}
	pad := " "
	if len(args) > 1 {
		pad = args[1].Text()
	}
	return Value{K: String, V: padString(pad, gap) + s}
}

// PadEnd — chuẩn JS String.padEnd(targetLength, padString).
func (v Value) PadEnd(args ...Value) Value {
	s := v.Text()
	target := 0
	if len(args) > 0 && args[0].K == Number {
		target = int(args[0].N)
	}
	gap := target - len([]rune(s))
	if gap <= 0 {
		return Value{K: String, V: s}
	}
	if target > maxRepeatBytes {
		return Value{K: Invalid, V: "padEnd: result too large (max 8MB)"}
	}
	pad := " "
	if len(args) > 1 {
		pad = args[1].Text()
	}
	return Value{K: String, V: s + padString(pad, gap)}
}

// TrimStart / TrimEnd — chuẩn JS.
func (v Value) TrimStart(_ ...Value) Value {
	return New(strings.TrimLeft(v.Text(), " \t\n\r\v\f"))
}

func (v Value) TrimEnd(_ ...Value) Value {
	return New(strings.TrimRight(v.Text(), " \t\n\r\v\f"))
}

// Concat — chuẩn JS String.concat(...).
func (v Value) Concat(args ...Value) Value {
	var b strings.Builder
	b.WriteString(v.Text())
	for _, a := range args {
		b.WriteString(a.Text())
	}
	return Value{K: String, V: b.String()}
}

// --- Array Methods (Mutation with *[]Value) ---

func (v Value) Push(args ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		*ptr = append(*ptr, args...)
	}
	return v
}

func (v Value) ItemAt(args ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok && len(args) > 0 {
		a := *ptr
		idx := int(args[0].N)
		if idx < 0 {
			idx = len(a) + idx
		}
		if idx >= 0 && idx < len(a) {
			return a[idx]
		}
	}
	return Value{K: Nil}
}

func (v Value) Pop(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok && len(*ptr) > 0 {
		idx := len(*ptr) - 1
		res := (*ptr)[idx]
		*ptr = (*ptr)[:idx]
		return res
	}
	return Value{K: Nil}
}

func (v Value) Shift(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok && len(*ptr) > 0 {
		res := (*ptr)[0]
		*ptr = (*ptr)[1:]
		return res
	}
	return Value{K: Nil}
}

func (v Value) Unshift(args ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		*ptr = append(args, *ptr...)
	}
	return v
}

func (v Value) Compact(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		a := *ptr
		res := make([]Value, 0)
		for _, item := range a {
			if item.K != Nil && item.Truthy() {
				res = append(res, item)
			}
		}
		*ptr = res
	}
	return v
}

func (v Value) Unique(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		a := *ptr
		seen := make(map[any]bool)
		res := make([]Value, 0)
		for _, item := range a {
			// Basic uniqueness by interface value
			key := item.Interface()
			if !seen[key] {
				seen[key] = true
				res = append(res, item)
			}
		}
		*ptr = res
	}
	return v
}

func (v Value) Reverse(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		a := *ptr
		for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
			a[i], a[j] = a[j], a[i]
		}
	}
	return v
}

func (v Value) Shuffle(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		a := *ptr
		rand.Shuffle(len(a), func(i, j int) {
			a[i], a[j] = a[j], a[i]
		})
	}
	return v
}

func (v Value) Random(args ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok && len(*ptr) > 0 {
		a := *ptr
		count := 1
		if len(args) > 0 {
			count = int(args[0].N)
		}

		if count <= 1 {
			return a[rand.Intn(len(a))]
		}

		// Multi-sample: return a new array
		if count > len(a) {
			count = len(a)
		}
		// Copy and shuffle to get 'count' unique items
		tmp := make([]Value, len(a))
		copy(tmp, a)
		rand.Shuffle(len(tmp), func(i, j int) {
			tmp[i], tmp[j] = tmp[j], tmp[i]
		})
		res := tmp[:count]
		return Value{K: Array, V: &res}
	}
	return Value{K: Nil}
}

func (v Value) Join(args ...Value) Value {
	sep := ","
	if len(args) > 0 {
		sep = args[0].Text()
	}
	if ptr, ok := v.V.(*[]Value); ok {
		var b strings.Builder
		for i, item := range *ptr {
			if i > 0 {
				b.WriteString(sep)
			}
			b.WriteString(item.Text())
		}
		return Value{K: String, V: b.String()}
	}
	return Value{K: String, V: ""}
}

// --- Number Methods ---

// ToFixed — chuẩn JS Number.toFixed(digits): trả CHUỖI với số chữ số thập phân cố định.
func (v Value) ToFixed(args ...Value) Value {
	digits := 0
	if len(args) > 0 && args[0].K == Number {
		digits = int(args[0].N)
	}
	if digits < 0 {
		digits = 0
	}
	if digits > 100 {
		digits = 100
	}
	return NewString(strconv.FormatFloat(v.N, 'f', digits, 64))
}

/* ---------------------------------------------------------------------------
   Các method Array chuẩn JS bổ sung (không cần callback — callback methods
   như forEach/some/every/reduce nằm trong VM vì cần thực thi Lambda).
   --------------------------------------------------------------------------- */

// ArraySlice — chuẩn JS Array.slice(start, end): trả MẢNG MỚI, hỗ trợ chỉ số âm.
func (v Value) ArraySlice(args ...Value) Value {
	a := v.Array()
	length := len(a)

	start := 0
	end := length
	if len(args) > 0 && args[0].K == Number {
		start = normalizeSliceIndex(int(args[0].N), length)
	}
	if len(args) > 1 && args[1].K == Number {
		end = normalizeSliceIndex(int(args[1].N), length)
	}
	if start >= end {
		return New([]Value{})
	}
	out := make([]Value, end-start)
	copy(out, a[start:end])
	return New(out)
}

// ArrayIndexOf — so sánh bằng Equal (deep), trả chỉ số hoặc -1.
func (v Value) ArrayIndexOf(args ...Value) Value {
	if len(args) == 0 {
		return Value{K: Number, N: -1}
	}
	for i, item := range v.Array() {
		if item.Equal(args[0]) {
			return Value{K: Number, N: float64(i)}
		}
	}
	return Value{K: Number, N: -1}
}

// ArrayLastIndexOf — chỉ số của lần xuất hiện cuối, -1 nếu không có.
func (v Value) ArrayLastIndexOf(args ...Value) Value {
	if len(args) == 0 {
		return Value{K: Number, N: -1}
	}
	a := v.Array()
	for i := len(a) - 1; i >= 0; i-- {
		if a[i].Equal(args[0]) {
			return Value{K: Number, N: float64(i)}
		}
	}
	return Value{K: Number, N: -1}
}

// ArrayIncludes — chuẩn JS Array.includes.
func (v Value) ArrayIncludes(args ...Value) Value {
	if len(args) == 0 {
		return FALSE
	}
	for _, item := range v.Array() {
		if item.Equal(args[0]) {
			return TRUE
		}
	}
	return FALSE
}

// ArrayConcat — chuẩn JS: trả MẢNG MỚI, đối số là mảng thì trải phẳng một cấp.
func (v Value) ArrayConcat(args ...Value) Value {
	src := v.Array()
	out := make([]Value, 0, len(src)+len(args))
	out = append(out, src...)
	for _, a := range args {
		if a.K == Array {
			out = append(out, a.Array()...)
		} else {
			out = append(out, a)
		}
	}
	return New(out)
}

func flattenInto(dst []Value, src []Value, depth int) []Value {
	for _, item := range src {
		if item.K == Array && depth > 0 {
			dst = flattenInto(dst, item.Array(), depth-1)
		} else {
			dst = append(dst, item)
		}
	}
	return dst
}

// ArrayFlat — chuẩn JS Array.flat(depth), mặc định depth = 1.
func (v Value) ArrayFlat(args ...Value) Value {
	depth := 1
	if len(args) > 0 && args[0].K == Number {
		depth = int(args[0].N)
	}
	out := flattenInto(make([]Value, 0, v.Len()), v.Array(), depth)
	return New(out)
}

// ArraySort — sort() KHÔNG comparator (có comparator thì VM xử lý vì cần Lambda).
// LỆCH CHUẨN CÓ CHỦ ĐÍCH: JS mặc định ép phần tử thành chuỗi rồi so sánh
// ([10, 2].sort() → [10, 2] — footgun nổi tiếng). Kitwork chọn hành vi hợp
// trực giác: toàn số → xếp theo số tăng dần; còn lại → xếp theo chuỗi.
// Sắp xếp TẠI CHỖ và trả về chính mảng (giống JS).
func (v Value) ArraySort(_ ...Value) Value {
	if ptr, ok := v.V.(*[]Value); ok {
		a := *ptr
		allNumbers := true
		for _, item := range a {
			if item.K != Number {
				allNumbers = false
				break
			}
		}
		if allNumbers {
			sort.SliceStable(a, func(i, j int) bool { return a[i].N < a[j].N })
		} else {
			sort.SliceStable(a, func(i, j int) bool { return a[i].Text() < a[j].Text() })
		}
	}
	return v
}

// --- Map Methods ---

func (v Value) Has(args ...Value) Value {
	if len(args) == 0 {
		return FALSE
	}
	if m, ok := v.V.(map[string]Value); ok {
		_, exists := m[args[0].String()]
		return ToBool(exists)
	}
	return FALSE
}

func (v Value) Keys(_ ...Value) Value {
	if m, ok := v.V.(map[string]Value); ok {
		keys := make([]Value, 0, len(m))
		for k := range m {
			keys = append(keys, Value{K: String, V: k})
		}
		return New(keys)
	}
	return Value{K: Array, V: &[]Value{}}
}

func (v Value) Delete(args ...Value) Value {
	if len(args) > 0 {
		if m, ok := v.V.(map[string]Value); ok {
			delete(m, args[0].Text())
		}
	}
	return v
}

func (v Value) Merge(args ...Value) Value {
	if len(args) > 0 && args[0].IsMap() {
		if m, ok := v.V.(map[string]Value); ok {
			other := args[0].Map()
			for k, val := range other {
				m[k] = val
			}
		}
	}
	return v
}
