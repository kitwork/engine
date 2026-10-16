package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kitwork/engine/builtins"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

// renderCondition renders {{ if condition }}T{{ else }}F{{ end }} as a template being developed, which
// writes a failure where the block would stand.
func renderCondition(condition string, data value.Value) string {
	return engineRender("{{ if "+condition+" }}T{{ else }}F{{ end }}", data, "", "")
}

// A {{ if }} compares the values of both sides of its whole condition (16/10/2026, the owner's E3). It
// read the first word as the value and the raw text after the operator as the other side, so each row
// below gave the other answer: the slug was compared with the text "slug" (the buildinpublic.guide navbar
// never marked the page it was on), !x looked up a key named "!x", only the first comparison of an ||
// counted (theinpublic.com's item pages lit no section), idx < list.length - 1 compared idx with the text
// "list.length" (an arrow after every step but the first on huynhnhanquoc.com), === was no operator (the
// name alone was read), and a quoted text was cut at its first space. CONTROL: the readings that were
// right stay right: a plain name, a name compared with a quoted word, a count compared with a number.
func TestIfComparesTheValuesOfBothSides(t *testing.T) {
	data := value.New(map[string]any{
		"slug":    "ast",
		"guide":   map[string]any{"file": map[string]any{"slug": "ast"}},
		"hidden":  false,
		"section": "launch",
		"steps":   []any{"idea", "build", "ship"},
		"title":   "two words",
		"count":   3,
	})
	for _, row := range []struct{ condition, want string }{
		{`guide.file.slug == slug`, "T"},
		{`guide.file.slug != slug`, "F"},
		{`!hidden`, "T"},
		{`section == 'launches' || section == 'launch'`, "T"},
		{`section == 'launch' && count == 4`, "F"},
		{`title == "two words"`, "T"},
		{`count === 4`, "F"},
		// CONTROL
		{`guide`, "T"},
		{`hidden`, "F"},
		{`section == 'launch'`, "T"},
		{`count > 2`, "T"},
		{`count == 4`, "F"},
	} {
		if got := renderCondition(row.condition, data); got != row.want {
			t.Errorf("{{ if %s }} = %q, want %q", row.condition, got, row.want)
		}
	}
	// the arrow between the steps, not after the last
	got := engineRender(`{{ for (index, step) in steps }}{{ step }}{{ if index < steps.length - 1 }} → {{ end }}{{ end }}`, data, "", "")
	if want := "idea → build → ship"; got != want {
		t.Errorf("steps = %q, want %q", got, want)
	}
}

// conditionCases are conditions a template and a script read alike: the template's {{ if }} and the
// VM's (cond) ? "T" : "F" over the same data give the same answer, which is want. FAIL is a failure,
// which ends the script's run and stands where the template's block would.
var conditionCases = []struct{ condition, want string }{
	// == and === are one operator: no conversion, numbers exactly (E1); != and !== its negation
	{`count == 3`, "T"},
	{`count === 3`, "T"},
	{`count == "3"`, "F"},
	{`one == 1`, "F"},
	{`one === "1"`, "T"},
	{`count != 3`, "F"},
	{`count !== "3"`, "T"},
	{`a + b == c`, "F"},
	{`a + b !== c`, "T"},
	{`a + b > c`, "T"},
	{`zero / zero != zero / zero`, "T"},
	{`NaN == NaN`, "F"},
	{`nothing == null`, "T"},
	{`nothing === undefined`, "T"},
	{`missing == null`, "T"},
	{`yes == true`, "T"},
	{`no === false`, "T"},
	{`empty == ""`, "T"},
	{`guide.file.slug == slug`, "T"},
	{`other.file.slug == slug`, "F"},
	{`guide.file == guide.file`, "T"},
	// texts in either quote, their escapes as a script reads them
	{`text == 'abc'`, "T"},
	{`"two words" == 'two words'`, "T"},
	{`'it\'s' == "it's"`, "T"},
	{`"a\"b" == 'a"b'`, "T"},
	{`"tab\there" == 'tab	here'`, "T"},
	// < > <= >= order numbers with numbers and texts with texts; null is 0, NaN is ordered with nothing
	{`count > 2`, "T"},
	{`count < 3`, "F"},
	{`count <= 3`, "T"},
	{`count >= 4`, "F"},
	{`idx < items.length - 1`, "T"},
	{`items.length > 0`, "T"},
	{`text < "abd"`, "T"},
	{`text > 1`, "F"},
	{`text < 1`, "F"},
	{`nothing < 1`, "T"},
	{`missing < 1`, "T"},
	{`zero / zero >= 0`, "F"},
	{`zero / zero <= 0`, "F"},
	{`-1 < zero`, "T"},
	{`count >= -3`, "T"},
	{`1e3 > 999`, "T"},
	{`2.5E-1 == 0.25`, "T"},
	{`0.5 == half`, "T"},
	// a number as JavaScript writes one: .5 and 1. (16/10/2026: .5 was the key "5" of the current data,
	// and 1. could not be read); ?. before a digit is a ternary and a number
	{`.5 == half`, "T"},
	{`-.5 < zero`, "T"},
	{`1. == 1`, "T"},
	{`1.e1 == 10`, "T"},
	{`yes ?.5 : zero`, "T"},
	{`no ?.5 : zero`, "F"},
	// && || ! and their order: ! first, then && before ||, each from the left
	{`yes && count`, "T"},
	{`no || zero`, "F"},
	{`no || count`, "T"},
	{`!no`, "T"},
	{`!count`, "F"},
	{`!!text`, "T"},
	{`!empty`, "T"},
	{`!missing`, "T"},
	{`!no && no`, "F"},
	{`!(no && no)`, "T"},
	{`yes || yes && no`, "T"},
	{`(yes || yes) && no`, "F"},
	{`section == 'launches' || section == 'launch'`, "T"},
	{`status == "draft" || status == "blocked"`, "T"},
	{`status == "draft" && status == "blocked"`, "F"},
	// arithmetic before comparison, * before +, each from the left
	{`count + count * 2 == 9`, "T"},
	{`count - 1 - 1 == 1`, "T"},
	{`count * 2 > count + 2`, "T"},
	{`count % 2 == 1`, "T"},
	{`-count < 0`, "T"},
	{`+one === 1`, "T"},
	// ?: and ??
	{`yes ? no : yes`, "F"},
	{`no ? yes : count`, "T"},
	{`missing ?? yes`, "T"},
	{`zero ?? yes`, "F"},
	// a member of a value and a method after a dot
	{`text.length == 3`, "T"},
	{`items.includes("b")`, "T"},
	{`items.includes(slug)`, "F"},
	{`guide.file.slug.length == 3`, "T"},
	// a failure is the condition's: a script's run ends there; a branch || and && never read does not fail
	{`text.nope()`, "FAIL"},
	{`text.nope() == 3`, "FAIL"},
	{`!text.nope()`, "FAIL"},
	{`no || text.nope()`, "FAIL"},
	{`yes || text.nope()`, "T"},
	{`no && text.nope()`, "F"},
	{`text - 1 > 0`, "FAIL"},
	{`-text < 0`, "FAIL"},
}

func conditionData() map[string]any {
	return map[string]any{
		"count": 3, "zero": 0, "half": 0.5, "idx": 1, "a": 0.1, "b": 0.2, "c": 0.3,
		"text": "abc", "one": "1", "empty": "", "yes": true, "no": false, "nothing": nil,
		"slug": "ast", "section": "launch", "status": "blocked",
		"guide": map[string]any{"file": map[string]any{"slug": "ast"}},
		"other": map[string]any{"file": map[string]any{"slug": "behavior"}},
		"items": []any{"a", "b", "c"},
	}
}

// The operators of a {{ if }} are the VM's (E3, with E1 and E2): each row is read by the template and
// by a script over the same data, and both give want. CONTROL: a name compared with a literal of its own
// kind (count == 3, count > 2, text == 'abc', yes == true) the old reading read right too.
func TestIfOperatorsAreTheVMs(t *testing.T) {
	data := conditionData()
	for _, row := range conditionCases {
		got := renderCondition(row.condition, value.New(data))
		if got != "T" && got != "F" {
			got = "FAIL"
		}
		if got != row.want {
			t.Errorf("template {{ if %s }} = %s (%q), want %s", row.condition, got, renderCondition(row.condition, value.New(data)), row.want)
		}
		if script := scriptCondition(t, row.condition, data); script != row.want {
			t.Errorf("script (%s) ? \"T\" : \"F\" = %s, want %s", row.condition, script, row.want)
		}
	}
}

// scriptCondition is what a script makes of condition over data: "T", "F" or "FAIL".
func scriptCondition(t *testing.T, condition string, data map[string]any) string {
	t.Helper()
	names := make([]string, 0, len(data))
	for name := range data {
		names = append(names, name)
	}
	sort.Strings(names)
	var source strings.Builder
	for _, name := range names {
		literal, err := json.Marshal(data[name])
		if err != nil {
			t.Fatal(err)
		}
		source.WriteString("const " + name + " = " + string(literal) + ";\n")
	}
	source.WriteString("const result = (" + condition + ") ? \"T\" : \"F\";\n")
	bytecode, err := compiler.CompileSource(source.String())
	if err != nil {
		t.Fatalf("compile %q: %v", condition, err)
	}
	vm := runtime.New(bytecode.Program)
	vm.MaxEnergy = 1_000_000
	builtins.InjectJSCompat(vm.Globals)
	if returned := vm.Run(); returned.IsInvalid() {
		return "FAIL"
	}
	return vm.Vars["result"].Text()
}

// A name in a condition is found as every template expression finds one: the names of for and let
// first, then the data, then the data of $name; .name is the current data's and . the current data.
// CONTROL: a name nothing binds is null.
func TestIfFindsNamesAsATemplatePathDoes(t *testing.T) {
	data := value.New(map[string]any{
		"current": "b",
		"items":   []any{map[string]any{"id": "a", "on": false}, map[string]any{"id": "b", "on": true}},
		"meta":    map[string]any{"type": "post"},
	})
	got := engineRender(`{{ let wanted = "b" }}{{ for item in items }}[{{ if item.id == current }}current{{ end }}`+
		`{{ if .on && .id == wanted }} on{{ end }}{{ if . == item }} self{{ end }}`+
		`{{ if $.meta.type == "post" }} post{{ end }}{{ if nobody == null }} null{{ end }}]{{ end }}`+
		`{{ if $meta.type === meta.type && meta.type == "post" }}[$meta]{{ end }}`, data, "", "")
	if want := "[ self post null][current on self post null][$meta]"; got != want {
		t.Fatalf("rendered\n%q\nwant\n%q", got, want)
	}
}

// A condition that cannot be read is said with where it stands and why, in the author's words: in a
// template a site serves, the template does not prepare (the site does not start, a broken edit is not
// published), as a script that does not compile; the place is the file the tag is in, relative to the
// site, its line and its column counted in characters. Only the first is said: the navbar's, which the
// page takes in before its own. CONTROL: the same site with the conditions written right prepares and
// renders.
func TestIfThatCannotBeReadDoesNotPrepare(t *testing.T) {
	site := func(navbar, page string) *Render {
		root := t.TempDir()
		for name, content := range map[string]string{
			"index.kitwork.html":     "<html><body>\n{{ @navbar }}{{ @page }}</body></html>",
			"navbar.kitwork.html":    navbar,
			"blog/page.kitwork.html": page,
		} {
			filename := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return New(Config{Base: root, Directory: ".", Path: "blog"}).Prepare()
	}
	navbar := "<nav>\n  <a class=\"{{ if ok }}on{{ end }}\">Ở đây</a> {{ if guide.slug == slug }}x{{ end }}\n</nav>"
	page := "<main>{{ if page == 1 }}first{{ end }}</main>"
	brokenNavbar := strings.Replace(navbar, "== slug", "==", 1)
	brokenPage := "<main>\n\t{{ if page === 1 && }}first{{ end }}</main>"

	for _, row := range []struct{ navbar, page, want string }{
		{brokenNavbar, brokenPage, "navbar.kitwork.html:2:66: {{ if guide.slug == }}: thiếu giá trị sau =="},
		{navbar, brokenPage, "blog/page.kitwork.html:2:21: {{ if page === 1 && }}: thiếu giá trị sau &&"},
	} {
		broken := site(row.navbar, row.page)
		err := broken.PreparationError()
		if err == nil {
			t.Fatalf("a condition that cannot be read prepared: %q", row.want)
		}
		if err.Error() != row.want {
			t.Fatalf("preparation error\n%q\nwant\n%q", err.Error(), row.want)
		}
		if got := broken.Bind(value.New(map[string]any{})).String(); got != err.Error() {
			t.Fatalf("a template that did not prepare rendered %q", got)
		}
	}

	// CONTROL
	fixed := site(navbar, page)
	if err := fixed.PreparationError(); err != nil {
		t.Fatalf("conditions written right did not prepare: %v", err)
	}
	got := fixed.Bind(value.New(map[string]any{"ok": true, "slug": "a", "guide": map[string]any{"slug": "a"}, "page": 1})).String()
	for _, want := range []string{`<a class="on">Ở đây</a> x`, "<main>first</main>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered %q, want it to hold %q", got, want)
		}
	}
}

// What a condition that cannot be read is told, at its place: line:column of the template rendered from
// a text (Render.HTML, which no file names), the tag as written, and why.
func TestIfThatCannotBeReadSaysWhy(t *testing.T) {
	data := value.New(map[string]any{})
	for _, row := range []struct{ template, want string }{
		{"{{ if }}", "1:7: {{ if }}: thiếu điều kiện: viết {{ if tên }} hoặc {{ if a == b }}"},
		{"{{ if a == }}", "1:11: {{ if a == }}: thiếu giá trị sau =="},
		{"{{ if == b }}", "1:7: {{ if == b }}: thiếu giá trị trước =="},
		{"{{ if !}}", "1:8: {{ if ! }}: thiếu giá trị sau !"},
		{"{{ if a b }}", `1:9: {{ if a b }}: thừa "b": hai giá trị cần một toán tử giữa chúng (==, ===, !=, <, >, &&, ||, …)`},
		{"{{ if a = b }}", "1:9: {{ if a = b }}: một dấu = là phép gán, điều kiện không gán: so sánh viết == hoặc ==="},
		{"{{ if a & b }}", "1:9: {{ if a & b }}: một dấu & không phải toán tử: và viết &&"},
		{"{{ if a | b }}", "1:9: {{ if a | b }}: một dấu | không phải toán tử: hoặc viết ||"},
		{"{{ if (a || b }}", "1:7: {{ if (a || b }}: dấu ( chưa có dấu ) đóng"},
		{"{{ if a) }}", "1:8: {{ if a) }}: dấu ) không có dấu ( mở"},
		{"{{ if a == 'x }}", "1:12: {{ if a == 'x }}: chuỗi mở bằng ' chưa đóng"},
		{"{{ if a ? b }}", "1:9: {{ if a ? b }}: a ? b : c thiếu phần : c"},
		{"{{ if a : b }}", "1:9: {{ if a : b }}: dấu : không có dấu ? trước nó (a ? b : c)"},
		{"{{ if count(a) }}", `1:12: {{ if count(a) }}: template gọi một hàm sau dấu chấm, như x.has("a"): count(…) không gọi được`},
		{"{{ if a[0] }}", "1:8: {{ if a[0] }}: template chưa đọc a[…]: một khoá viết a.khoá"},
		{"{{ if a.has('x',) }}", "1:17: {{ if a.has('x',) }}: thừa dấu phẩy trước )"},
		{"{{ if a.has('x' }}", "1:12: {{ if a.has('x' }}: lời gọi mở bằng ( chưa có ) đóng"},
		{"{{ if a. }}", "1:9: {{ if a. }}: thiếu tên sau dấu chấm"},
		{"{{ if user?.name }}", "1:11: {{ if user?.name }}: template chưa có ?.: viết a.khoá, khoá của null đã là null"},
		{"{{ if 3x }}", `1:7: {{ if 3x }}: không đọc được "3x": một tên không bắt đầu bằng chữ số`},
		{"{{ if `x` }}", "1:7: {{ if `x` }}: template chưa có chuỗi `…`: viết trong nháy đơn hoặc nháy kép"},
		{"<p>\n  Ở đây {{ if ok }}{{ end }}\n  {{ if\n    a ==\n    }}", "4:9: {{ if a == }}: thiếu giá trị sau =="},
		{"{{ if " + strings.Repeat("(", 70) + "a" + strings.Repeat(")", 70) + " }}", "1:71: {{ if " + strings.Repeat("(", 70) + "a" + strings.Repeat(")", 70) + " }}: điều kiện lồng quá sâu (quá 64 tầng)"},
	} {
		var heard []string
		production := New(Config{ExpressionFailed: func(expression, message string) { heard = append(heard, message) }})
		if got := production.HTML(row.template+"T{{ end }}", data); strings.Contains(got, "T") || strings.Contains(got, "{{") {
			t.Errorf("%q wrote %q in production", row.template, got)
		}
		if len(heard) != 1 || heard[0] != row.want {
			t.Errorf("%q said\n%q\nwant\n%q", row.template, heard, row.want)
		}
	}
}

// A condition that fails is told as a {{ expression }} that fails is (14/10/2026): while developing its
// message stands where the block would, in production the page writes nothing there and the message
// goes to the log (Config.ExpressionFailed). Neither branch renders, as a script's run ends at the
// failure; until 16/10/2026 the failure was false and the else branch rendered, with nothing said.
// CONTROL: the condition that does not fail renders its branch in both, and a failing output beside it
// is told as it was.
func TestIfThatFailsIsToldAsAFailedExpressionIs(t *testing.T) {
	data := value.New(map[string]any{"item": map[string]any{"name": "ab"}, "ok": true})
	template := `[{{ if item.nope() == 3 }}yes{{ else }}no{{ end }}|{{ if item.name - 1 }}yes{{ else }}no{{ end }}|{{ if a b }}yes{{ else }}no{{ end }}|{{ if ok }}ok{{ end }}|{{ item.nope() }}]`

	development := New(Config{}).HTML(template, data)
	want := "[item.nope không phải hàm (là null): không gọi được|phép - không dùng được giữa chuỗi và số|1:107: {{ if a b }}: thừa &#34;b&#34;: hai giá trị cần một toán tử giữa chúng (==, ===, !=, &lt;, &gt;, &amp;&amp;, ||, …)|ok|item.nope không phải hàm (là null): không gọi được]"
	if development != want {
		t.Errorf("while developing\n%q\nwant\n%q", development, want)
	}

	type heard struct{ expression, message string }
	var log []heard
	production := New(Config{ExpressionFailed: func(expression, message string) { log = append(log, heard{expression, message}) }}).HTML(template, data)
	if want := "[|||ok|]"; production != want {
		t.Errorf("in production\n%q\nwant\n%q", production, want)
	}
	wantLog := []heard{
		{"if item.nope() == 3", "item.nope không phải hàm (là null): không gọi được"},
		{"if item.name - 1", "phép - không dùng được giữa chuỗi và số"},
		{"if a b", `1:107: {{ if a b }}: thừa "b": hai giá trị cần một toán tử giữa chúng (==, ===, !=, <, >, &&, ||, …)`},
		{"item.nope()", "item.nope không phải hàm (là null): không gọi được"},
	}
	if len(log) != len(wantLog) {
		t.Fatalf("logged %q, want %q", log, wantLog)
	}
	for index := range wantLog {
		if log[index] != wantLog[index] {
			t.Errorf("logged %q, want %q", log[index], wantLog[index])
		}
	}
}

// A failure an operand meets is the failure of the expression it stands in, in a {{ expression }} as in
// a condition (16/10/2026, with E3), as a script's run ends there: {{ item.nope() == 3 }} wrote false,
// {{ item.nope() || "x" }} and {{ item.nope() ?? "x" }} wrote x, and {{ item.nope() ? 1 : 2 }} wrote 2,
// with nothing said. CONTROL: operands that do not fail, || past a value that is only false, ?? past
// null.
func TestAnOperandsFailureIsTheExpressions(t *testing.T) {
	data := value.New(map[string]any{"item": map[string]any{"n": 3, "off": false}})
	got := engineRender(`[{{ item.nope() == 3 }}|{{ 3 != item.nope() }}|{{ item.nope() || "x" }}|{{ item.nope() ?? "x" }}|{{ item.nope() ? 1 : 2 }}|`+
		`{{ item.n == 3 }}|{{ item.off || "x" }}|{{ item.missing ?? "x" }}|{{ item.off ? 1 : 2 }}]`, data, "", "")
	failure := "item.nope không phải hàm (là null): không gọi được"
	want := "[" + strings.Repeat(failure+"|", 5) + "true|x|x|2]"
	if got != want {
		t.Fatalf("rendered\n%q\nwant\n%q", got, want)
	}
}
