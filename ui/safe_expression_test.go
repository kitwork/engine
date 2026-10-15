package ui

import "testing"

// An expression of .ui that calls safe is a compile error at its place (14/10/2026): an expression has
// nothing to catch (it never fails as it runs, on either end), safe would have to ship in the browser's
// kernel for a function that never has work, and logic belongs in .work. The view reads the failure
// with safe(() => …) and the page shows that data. Until then {{ safe(() => x) }} compiled and printed
// nothing. CONTROL: the data a view read with safe, and a lambda whose parameter is named safe, compile.
func TestAnExpressionThatCallsSafeIsACompileError(t *testing.T) {
	const says = "biểu thức .ui không có lỗi để bắt: gọi safe(() => …) trong router.view rồi đọc kết quả"
	mustFail(t, content(`<content><text>{{ safe(() => total) }}</text></content>`), "content.ui:1:", says)
	mustFail(t, content(`<content><box show="safe(() => ready).value"><text>x</text></box></content>`), "content.ui:1:15", says)
	mustFail(t, content(`<content><button click="count = safe(() => count + 1).value">+</button></content>`), "content.ui:1:18", says)
	// deep in an expression: the first argument of a call, an element of an array
	mustFail(t, content(`<content><text>{{ money(safe(() => total).value) }}</text></content>`), "content.ui:1:", says)
	mustFail(t, content(`<content><text>{{ [safe(() => 1), 2].length }}</text></content>`), "content.ui:1:", says)

	// CONTROL
	html := mustCompile(t, content(`<content><error>{{ productError.message }}</error><text>{{ found.value }}</text><text>{{ [1].map((safe) => safe(1)) }}</text><text>đã dịch</text></content>`))
	mustContain(t, html, "đã dịch")
}
