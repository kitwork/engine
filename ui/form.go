package ui

import "strings"

// <form> sends what its fields hold to the page it stands on (decision of 20/10/2026). It writes no
// action, so the browser sends it to the page's own address, where router.post in the router.work of
// that folder receives it. method says how: get, the browser's default (the fields in the address; Drive
// moves to it as it does on a link), or post (the fields in the body; Drive steps aside and the browser
// sends it itself: jit/hydrate/drive.js, the submit listener). method is a word written in the markup,
// never data. A form of .ui sends its fields urlencoded: enctype, the upload of a file, is not built yet.

// methodProblem is the error of a method a form does not take, naming the one to write (get for a GET
// written in capitals, post otherwise); "" for get and post.
func methodProblem(value string) string {
	switch value {
	case "get", "post":
		return ""
	}
	write := "post"
	if strings.EqualFold(value, "get") {
		write = "get"
	}
	return `method của form là get hay post: <form method="` + write + `">`
}

// formWords reads the words of a form (take, the tag's own attributes) and writes method as written. A
// form inside a form is refused at the inner one, however deep (through a box, a field, a layout piece):
// a browser drops the inner <form> and its fields go with the outer one, by the outer one's method.
func (c *compiler) formWords(n *Node, take func(string) *Attr, w *strings.Builder, cx ctx) error {
	for f := cx.paint; f != nil; f = f.parent {
		if f.html == "form" && f.node != nil {
			return errAt(n, "form không đặt trong form: trình duyệt bỏ <form> bên trong, các ô của nó gửi theo form ngoài (<form> ở %s:%d:%d); đặt hai form cạnh nhau",
				f.node.File, f.node.Line, f.node.Column)
		}
	}
	if a := take("method"); a != nil {
		if strings.Contains(a.Value, "{{") {
			return errAttr(n, a, "method của form là chữ cố định (get hay post), không lấy từ dữ liệu")
		}
		value := strings.TrimSpace(a.Value)
		if problem := methodProblem(value); problem != "" {
			return errAttr(n, a, "%s", problem)
		}
		writeAttr(w, "method", value)
	}
	if a := take("action"); a != nil {
		return errAttr(n, a, "action chưa có: form gửi về chính trang này, router.post trong router.work của thư mục nhận nó")
	}
	if a := take("enctype"); a != nil {
		return errAttr(n, a, "enctype chưa có: form của .ui gửi dạng urlencoded; tải file lên chưa có")
	}
	return nil
}
