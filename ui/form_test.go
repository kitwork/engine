package ui

import (
	"strings"
	"testing"
)

// <form> sends its fields to the page it stands on (decision of 20/10/2026, form.go): method is get or
// post, written in the markup, and written on the form as it is; without it the browser's default, get.
// action and enctype are not built yet and say so: the form goes back to its own page, where router.post
// in the router.work of that folder receives it, urlencoded. Each refusal is at its attribute, in its
// words; method written where nothing reads it is read the way a form reads it, then left out with a
// warning.
// CONTROL: with the form's case left out of tagAttributes (formWords never called) method on a form was
// a word that does nothing there (a warning, no method written) and a method from data compiled, and
// this test failed; with formWords writing no method the output rows failed (run 20/10/2026 on overlaid
// copies of compile.go and form.go).
func TestFormMethod(t *testing.T) {
	const button = `<button type="submit">Gửi</button>`
	const sent = `<button class="flex-row background-ink/6 hover:background-ink/10 pressed:background-ink/14 disabled:background-ink/4" type="submit">Gửi</button>`
	for _, c := range []struct{ words, want string }{
		{` method="post"`, `<form class="flex-column" method="post">`},
		{` method=" get "`, `<form class="flex-column" method="get">`},
		{``, `<form class="flex-column">`},
		{` method="post" name="dat-ban" gap="2"`, `<form class="flex-column gap-2" method="post" name="dat-ban">`},
	} {
		html := compileQuiet(t, map[string]string{"/site/content.ui": `<content><form` + c.words + `>` + button + `</form></content>`})
		if want := `<main class="flex-column">` + c.want + sent + `</form></main>`; !strings.Contains(html, want) {
			t.Errorf("<form%s>:\n got  %s\n want %s", c.words, html, want)
		}
	}

	const choose = `method của form là get hay post: <form method="post">`
	for _, c := range []struct{ content, needle, says string }{
		{`<content><form method="put">` + button + `</form></content>`, "method=", choose},
		{`<content><form method="dialog">` + button + `</form></content>`, "method=", choose},
		{`<content><form method="">` + button + `</form></content>`, "method=", choose},
		{`<content><form method="POST">` + button + `</form></content>`, "method=", choose},
		{`<content><form method="GET">` + button + `</form></content>`, "method=", `method của form là get hay post: <form method="get">`},
		{`<content><form method="{{ how }}">` + button + `</form></content>`, "method=", `method của form là chữ cố định (get hay post), không lấy từ dữ liệu`},
		{`<content><form method="post" action="/dat-ban">` + button + `</form></content>`, "action=",
			`action chưa có: form gửi về chính trang này, router.post trong router.work của thư mục nhận nó`},
		{`<content><form method="post" enctype="multipart/form-data">` + button + `</form></content>`, "enctype=",
			`enctype chưa có: form của .ui gửi dạng urlencoded; tải file lên chưa có`},
		// written where nothing reads it, a method is still read as a form reads it
		{`<content><box method="pots"><text>x</text></box></content>`, "method=", choose},
	} {
		mustFailExactly(t, map[string]string{"/site/content.ui": c.content}, where("content.ui", c.content, c.needle)+c.says)
	}
	box := `<content><box method="post"><text>x</text></box></content>`
	compileWarns(t, map[string]string{"/site/content.ui": box}, where("content.ui", box, "method="),
		"method không có tác dụng trên <box>; trang hiện như không viết nó")
}

// A form inside a form is refused at the inner one, however deep (right inside, in a box, through a
// layout piece): a browser drops the inner <form>, so its fields would go with the outer one, by the
// outer one's method, to the outer one's handler (a search sent as a post). The message names the outer
// form at its place. Two forms side by side compile.
//
// CONTROL: with formWords no longer looking for a form around (its loop over cx.paint skipped), each of
// the four forms inside a form compiled, and this test failed.
func TestFormInsideAFormIsRefused(t *testing.T) {
	const search = `<form method="get"><field><label>Tìm</label><input name="q" /></field><button type="submit">Tìm</button></form>`
	const note = `<form method="post"><field><label>Lời nhắn</label><input name="note" /></field><button type="submit">Gửi</button></form>`
	refusal := func(source string) string {
		return "form không đặt trong form: trình duyệt bỏ <form> bên trong, các ô của nó gửi theo form ngoài (<form> ở " +
			strings.TrimSuffix(where("content.ui", source, "<form"), ": ") + "); đặt hai form cạnh nhau"
	}
	// mustFailExactly checks every row and goes on, so a check disabled shows each row it guards
	mustFailExactly := func(t *testing.T, files map[string]string, want string) {
		t.Helper()
		if _, err := memorySite("", files).Compile("/site"); err == nil || err.Error() != want {
			t.Errorf("want the error\n  %s\ngot\n  %v", want, err)
		}
	}
	for _, content := range []string{
		`<content><form method="post">` + search + `</form></content>`,
		`<content><form method="post"><box gap="2">` + search + `</box></form></content>`,
		`<content><form><card><box>` + note + `</box></card></form></content>`,
	} {
		inner := strings.Index(content, "<form")
		inner += 1 + strings.Index(content[inner+1:], "<form")
		mustFailExactly(t, map[string]string{"/site/content.ui": content},
			where("content.ui", content[:inner]+"\x00"+content[inner:], "\x00")+refusal(content))
	}
	// through a layout piece whose root is a form
	content := `<content><form method="post"><box><site-search /></box></form></content>`
	mustFailExactly(t, map[string]string{"/site/content.ui": content, "/site/site-search.ui": search},
		where("site-search.ui", search, "<form")+refusal(content))
	// control: side by side, each sends its own
	html := compileQuiet(t, map[string]string{"/site/content.ui": `<content>` + search + note + `</content>`})
	if strings.Count(html, "<form ") != 2 || !strings.Contains(html, `method="get"`) || !strings.Contains(html, `method="post"`) {
		t.Errorf("two forms side by side:\n%s", html)
	}
}
