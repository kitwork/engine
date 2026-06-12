package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// TestJSCompatGlobalsGoLevel kiểm tra trực tiếp Math/Date ở mức value.Value.
func TestJSCompatGlobalsGoLevel(t *testing.T) {
	globals := map[string]value.Value{}
	injectJSCompat(globals)

	mathObj, ok := globals["Math"]
	if !ok {
		t.Fatal("Math global not found")
	}
	if got := mathObj.Invoke("floor", value.New(3.9)); got.N != 3 {
		t.Errorf("Math.floor(3.9) = %v, want 3", got.N)
	}
	if got := mathObj.Invoke("round", value.New(2.5)); got.N != 3 {
		t.Errorf("Math.round(2.5) = %v, want 3", got.N)
	}
	if got := mathObj.Invoke("max", value.New(1), value.New(9), value.New(4)); got.N != 9 {
		t.Errorf("Math.max(1,9,4) = %v, want 9", got.N)
	}
	if got := mathObj.Get("PI"); got.N < 3.14 || got.N > 3.15 {
		t.Errorf("Math.PI = %v", got.N)
	}

	dateVal, ok := globals["Date"]
	if !ok {
		t.Fatal("Date global not found")
	}

	// Date.now() — static property trên FuncObject
	now := dateVal.Invoke("now")
	if now.N < 1.7e12 {
		t.Errorf("Date.now() = %v, expected epoch ms > 1.7e12", now.N)
	}

	// Date() — gọi như constructor trả về date object
	d := dateVal.Call("Date")
	if d.K != value.Map {
		t.Fatalf("Date() returned kind %v, want Map", d.K)
	}
	iso := d.Invoke("toISOString")
	if !strings.Contains(iso.Text(), "T") || !strings.HasSuffix(iso.Text(), "Z") {
		t.Errorf("toISOString() = %q, not ISO format", iso.Text())
	}
	year := d.Invoke("getFullYear")
	if year.N < 2020 {
		t.Errorf("getFullYear() = %v", year.N)
	}

	// new Date(ms) tương đương Date(ms)
	fixed := dateVal.Call("Date", value.New(1749720000000))
	if got := fixed.Invoke("getTime"); got.N != 1749720000000 {
		t.Errorf("Date(ms).getTime() = %v, want 1749720000000", got.N)
	}
}

// TestJSCompatScriptLevel chạy script thật qua toàn bộ pipeline
// lexer → parser → compiler → VM: dùng `new Date()`, `%`, `Math.*`, `Date.now()`.
func TestJSCompatScriptLevel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-jscompat-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	scriptCode := `
const ms = Date.now();
const d = new Date();
const fixed = new Date(2026, 5, 12);
const remainder = 17 % 5;
const half = Math.floor(9 / 2);
const biggest = Math.max(3, 11, 7);
console.log("JSCompat:", ms, d.getFullYear(), fixed.getMonth(), remainder, half, biggest, d.toISOString());
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("script using new Date / %% / Math failed to run: %v", err)
	}
}

// TestJSCompatOperators kiểm tra ===, !==, ternary, +=, -=, *=, /=, ++, --
// chạy qua toàn bộ pipeline thật.
func TestJSCompatOperators(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-jsops-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	scriptCode := `
const strict = 5 === 5;
const strictNot = 5 !== 4;
const label = strict ? "yes" : "no";
const nested = 1 === 2 ? "a" : 3 === 3 ? "b" : "c";

let total = 10;
total += 5;
total -= 3;
total *= 4;
total /= 2;

let counter = 0;
counter++;
counter++;
counter--;
++counter;

console.log("JSOps:", strict, strictNot, label, nested, total, counter);
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("script using ===, ternary, +=, ++ failed to run: %v", err)
	}
	// Kỳ vọng output: true true yes b 24 2
}

// TestJSCompatStringMethods kiểm tra các method String chuẩn JS ở mức Go-level
// với cả chuỗi tiếng Việt (index theo rune, không vỡ ký tự có dấu).
func TestJSCompatStringMethods(t *testing.T) {
	s := func(str string) value.Value { return value.NewString(str) }
	n := func(num float64) value.Value { return value.New(num) }

	cases := []struct {
		name string
		got  value.Value
		want string
	}{
		{"slice", s("hello world").Slice(n(6)), "world"},
		{"slice negative", s("hello world").Slice(n(-5)), "world"},
		{"slice vietnamese", s("Phường Bến Nghé").Slice(n(0), n(6)), "Phường"},
		{"substring swap", s("hello").Substring(n(3), n(1)), "el"},
		{"charAt vietnamese", s("Phường").CharAt(n(2)), "ư"},
		{"at negative", s("hello").StrAt(n(-1)), "o"},
		{"repeat", s("ab").Repeat(n(3)), "ababab"},
		{"padStart", s("5").PadStart(n(3), s("0")), "005"},
		{"padEnd", s("5").PadEnd(n(3), s("0")), "500"},
		{"trimStart", s("  xy  ").TrimStart(), "xy  "},
		{"trimEnd", s("  xy  ").TrimEnd(), "  xy"},
		{"replace first only", s("a-a-a").Replace(s("a"), s("b")), "b-a-a"},
		{"replaceAll", s("a-a-a").ReplaceAll(s("a"), s("b")), "b-b-b"},
		{"concat", s("Kit").Concat(s("Data"), s(".vn")), "KitData.vn"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.Text() != tc.want {
				t.Errorf("got %q, want %q", tc.got.Text(), tc.want)
			}
		})
	}

	// indexOf trả chỉ số rune
	if idx := s("Phường Bến Nghé").IndexOf(s("Bến")); idx.N != 7 {
		t.Errorf("indexOf rune index = %v, want 7", idx.N)
	}
	if idx := s("a-b-a").LastIndexOf(s("a")); idx.N != 4 {
		t.Errorf("lastIndexOf = %v, want 4", idx.N)
	}
	if idx := s("hello").IndexOf(s("z")); idx.N != -1 {
		t.Errorf("indexOf missing = %v, want -1", idx.N)
	}
	// charCodeAt
	if c := s("A").CharCodeAt(n(0)); c.N != 65 {
		t.Errorf("charCodeAt = %v, want 65", c.N)
	}
	// length theo rune
	if l := s("Phường").Get("length"); l.N != 6 {
		t.Errorf("'Phường'.length = %v, want 6", l.N)
	}
	// split() không đối số trả nguyên chuỗi
	if parts := s("abc").Split(); parts.Len() != 1 {
		t.Errorf("split() len = %v, want 1", parts.Len())
	}
	// repeat quá lớn phải bị chặn (bảo vệ bộ nhớ multi-tenant)
	if r := s("xxxxxxxxxx").Repeat(n(10000000)); r.K != value.Invalid {
		t.Errorf("huge repeat should return Invalid, got kind %v", r.K)
	}
}

// TestJSCompatStringScript chạy method string qua pipeline VM thật.
func TestJSCompatStringScript(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-jsstr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	scriptCode := `
const ward = "Phường Bến Nghé";
const upper = ward.toUpperCase();
const found = ward.indexOf("Bến");
const piece = ward.slice(0, 6);
const code = "SP-001-VN".split("-");
const padded = "7".padStart(3, "0");
const swapped = "a-a-a".replace("a", "b");
const all = "a-a-a".replaceAll("a", "b");
console.log("JSStr:", upper, found, piece, ward.length, code.length, code[1], padded, swapped, all);
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("string methods script failed: %v", err)
	}
	// Kỳ vọng: PHƯỜNG BẾN NGHÉ 7 Phường 15 3 001 007 b-a-a b-b-b
}

// TestJSCompatArrayMethods kiểm tra method Array không-callback ở Go-level.
func TestJSCompatArrayMethods(t *testing.T) {
	n := func(num float64) value.Value { return value.New(num) }
	arr := func(nums ...float64) value.Value {
		items := make([]value.Value, len(nums))
		for i, x := range nums {
			items[i] = value.New(x)
		}
		return value.New(items)
	}

	// slice trả mảng mới, hỗ trợ chỉ số âm
	if got := arr(1, 2, 3, 4, 5).ArraySlice(n(1), n(3)); got.Len() != 2 || got.Index(0).N != 2 {
		t.Errorf("slice(1,3) = %v", got.Text())
	}
	if got := arr(1, 2, 3).ArraySlice(n(-2)); got.Len() != 2 || got.Index(0).N != 2 {
		t.Errorf("slice(-2) = %v", got.Text())
	}
	// indexOf / lastIndexOf / includes
	if got := arr(5, 1, 4).ArrayIndexOf(n(4)); got.N != 2 {
		t.Errorf("indexOf(4) = %v, want 2", got.N)
	}
	if got := arr(1, 2, 1).ArrayLastIndexOf(n(1)); got.N != 2 {
		t.Errorf("lastIndexOf(1) = %v, want 2", got.N)
	}
	if got := arr(1, 2).ArrayIncludes(n(9)); got.Truthy() {
		t.Error("includes(9) should be false")
	}
	// concat không mutate, trải phẳng đối số mảng một cấp
	a := arr(1, 2)
	combined := a.ArrayConcat(arr(3, 4), n(5))
	if combined.Len() != 5 || a.Len() != 2 {
		t.Errorf("concat: len=%d (want 5), original len=%d (want 2)", combined.Len(), a.Len())
	}
	// flat với depth
	nested := value.New([]value.Value{n(1), value.New([]value.Value{n(2), value.New([]value.Value{n(3)})})})
	if got := nested.ArrayFlat(n(2)); got.Len() != 3 {
		t.Errorf("flat(2) len = %d, want 3", got.Len())
	}
	// sort mặc định: toàn số xếp theo số (khác footgun của JS)
	sorted := arr(10, 2, 33, 4).ArraySort()
	if sorted.Index(0).N != 2 || sorted.Index(3).N != 33 {
		t.Errorf("sort numeric = %v", sorted.Text())
	}
}

// TestJSCompatArrayScript chạy các method Array (gồm cả callback: forEach,
// some, every, reduce, findIndex, sort comparator) qua pipeline VM thật.
func TestJSCompatArrayScript(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-jsarr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	scriptCode := `
const nums = [5, 1, 4, 2, 3];
const total = nums.reduce((acc, x) => acc + x, 0);
const anyBig = nums.some(x => x > 4);
const allPos = nums.every(x => x > 0);
const idx4 = nums.findIndex(x => x === 4);
const sorted = [10, 2, 33, 4].sort();
const desc = [10, 2, 33, 4].sort((a, b) => b - a);
const flatJoin = [1, [2, [3]]].flat(2).join("-");
let sum = 0;
nums.forEach(x => { sum += x; });
console.log("JSArr:", total, anyBig, allPos, idx4, sorted.join(","), desc.join(","), flatJoin, sum, nums.includes(3), nums.indexOf(4), nums.slice(1, 3).join(","));
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("array methods script failed: %v", err)
	}
	// Kỳ vọng: 15 true true 2 2,4,10,33 33,10,4,2 1-2-3 15 true 2 1,4
}

// TestNestedClosureScope kiểm tra lexical scoping nhiều cấp: lambda lồng 2-3
// cấp phải đọc/ghi được biến của các hàm bao ngoài (pattern forEach trong
// forEach push vào mảng kết quả — như handler search thực tế của tenant).
func TestNestedClosureScope(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-closure-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	scriptCode := `
const runSearch = (query) => {
    const groups = { a: ["Tai Nghe", "Ban Phim"], b: ["Kem Chong Nang"] };
    const results = [];
    const keys = ["a", "b"];
    keys.forEach((key) => {
        const list = groups[key];
        list.forEach((item) => {
            if (query == "" || item.indexOf(query) != -1) {
                results.push(item);
            }
        });
    });
    return results;
};

const all = runSearch("");
const hit = runSearch("Tai");

let depth3 = 0;
const outer = () => {
    const mid = () => {
        const inner = () => {
            depth3 += 7;
        };
        inner();
    };
    mid();
};
outer();

console.log("Closure:", all.length, hit.length, hit.join(","), depth3);
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("nested closure script failed: %v", err)
	}
	// Kỳ vọng: 3 1 Tai Nghe 7
}

// TestJSCompatObjectNumberGlobals kiểm tra Object/Number/String/Boolean globals
// và Number.toFixed qua pipeline VM thật.
func TestJSCompatObjectNumberGlobals(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kitwork-jsobj-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	tenantDir := filepath.Join(tmpDir, "test", "localhost")
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		t.Fatal(err)
	}

	scriptCode := `
const o = { a: 1, b: 2 };
const ks = Object.keys(o).sort().join(",");
const vs = Object.values(o).sort().join(",");
const merged = Object.assign({}, o, { c: 3 });
const back = Object.fromEntries(Object.entries(o));
const entryCount = Object.entries(o).length;

const n1 = Number("42.5");
const n2 = Number.parseInt("99.9");
const isInt = Number.isInteger(7);
const notInt = Number.isInteger(7.5);
const fx = (3.14159).toFixed(2);

const s1 = String(123);
const ch = String.fromCharCode(75, 105, 116);
const b1 = Boolean("");
const b2 = Boolean("x");

console.log("JSObj:", ks, vs, merged.c, back.a, entryCount, n1, n2, isInt, notInt, fx, s1, ch, b1, b2);
`
	if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(scriptCode), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmpDir, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatalf("Object/Number globals script failed: %v", err)
	}
	// Kỳ vọng: a,b 1,2 3 1 2 42.5 99 true false 3.14 123 Kit false true
}

// TestReservedKeywordsRejected đảm bảo while/try bị từ chối kèm thông báo
// hướng dẫn — đúng triết lý thiết kế: loại bỏ vòng lặp vô tận và try/catch.
func TestReservedKeywordsRejected(t *testing.T) {
	cases := []struct {
		name    string
		script  string
		keyword string
	}{
		{"while", `while (true) { console.log("x"); }`, "while"},
		{"try", `try { console.log("x"); } catch (e) {}`, "try"},
		{"switch", `switch (x) {}`, "switch"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "kitwork-reserved-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			tenantDir := filepath.Join(tmpDir, "test", "localhost")
			if err := os.MkdirAll(tenantDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tenantDir, "app.kitwork.js"), []byte(tc.script), 0644); err != nil {
				t.Fatal(err)
			}

			tenant := NewTenant(tmpDir, "localhost")
			runErr := tenant.Run()
			if runErr == nil {
				t.Fatalf("expected compile error for '%s', got nil", tc.keyword)
			}
			if !strings.Contains(runErr.Error(), tc.keyword) {
				t.Errorf("error should mention keyword %q, got: %v", tc.keyword, runErr)
			}
		})
	}
}
