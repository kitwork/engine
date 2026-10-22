package work

// The order of a response in router.work (KITWORK_ROUTER §6, §11; 20/10/2026): every edit stands
// before the one answer that ends the chain.
//
//	return context.title("Sản phẩm").description("Danh mục").view({ products });
//	return context.status(201).json(created);
//	return context.type("image/png").file(pngBytes);
//
// An edit after the answer (context.view({…}).title("x"), context.json(x).status(201)), two answers in
// one chain, the page's text before an answer other than view, and a .type() against an answer that
// has its own media type (json, view) are errors that show the order that works. It is checked only
// while the site's own program runs (a handler, a guard, a hook): the engine answering after it (the
// page it draws, notfound.ui) is not the program's chain. A template site (router.kitwork.js) keeps
// every way it has, a title after the view included.

import "fmt"

// responseOrder is where a router.work response stands in its chain.
type responseOrder struct {
	inProgram  bool   // the site's program is running (execTree, serveUIView)
	answeredBy string // the answer that ended the chain, "" before it
	pageText   string // the first of the page's text written (title, description…), "" when none
	typed      bool   // .type() was written
}

// pageTextEdits are the edits that are the page's text: they mean something only to .view().
var pageTextEdits = map[string]bool{
	"title": true, "description": true, "image": true, "canonical": true, "robots": true, "meta": true,
	"url": true, "language": true, "jsonld": true,
}

// drawsPage are the answers that draw a page (content.ui, notfound.ui, error.ui): the page's text may
// stand before them (20/10/2026: notFound and error too, context.title("Không tìm thấy").notFound()).
var drawsPage = map[string]bool{"view": true, "notFound": true, "error": true}

// ownMediaType are the answers whose media type is theirs: a .type() against them is an error.
var ownMediaType = map[string]bool{"json": true, "view": true}

// ordered reports whether the order is checked: the site is router.work and its program is running.
func (r *Response) ordered() bool {
	return r.order.inProgram && r.inUISite()
}

// editing checks an edit: before the answer.
func (r *Response) editing(edit string) error {
	if !r.ordered() {
		return nil
	}
	if r.order.answeredBy != "" {
		return fmt.Errorf("`.%s()` nối sau `.%s()`: mọi chỉnh sửa đứng trước hàm trả lời: `context.%s(…).%s(…)`",
			edit, r.order.answeredBy, edit, r.order.answeredBy)
	}
	if pageTextEdits[edit] && r.order.pageText == "" {
		r.order.pageText = edit
	}
	if edit == "type" {
		r.order.typed = true
	}
	return nil
}

// answering checks an answer: one, last, matching the edits before it.
func (r *Response) answering(answer string) error {
	if !r.ordered() {
		return nil
	}
	switch {
	case r.order.answeredBy != "":
		return fmt.Errorf("`.%s()` sau `.%s()`: một chuỗi chỉ có một hàm trả lời, ở cuối: các chỉnh sửa đứng trước nó, `context.status(…).%s(…)`",
			answer, r.order.answeredBy, r.order.answeredBy)
	case r.order.pageText != "" && !drawsPage[answer]:
		return fmt.Errorf("`.%s()` là chữ của trang, chỉ đi với hàm trả lời vẽ trang (`.view()`, `.notFound()`, `.error()`): bỏ `.%s()` trước `.%s()`, hoặc trả lời bằng `context.%s(…).view({ … })`",
			r.order.pageText, r.order.pageText, answer, r.order.pageText)
	case r.order.typed && ownMediaType[answer]:
		return fmt.Errorf("`.type()` không hợp với `.%s()`, câu trả lời có media type của nó: bỏ `.type()`, hoặc trả lời bằng `.type(…).text(…)` hay `.type(…).file(…)`",
			answer)
	}
	r.order.answeredBy = answer
	return nil
}

// runProgram marks the response as the program's while run runs: the order is checked then only.
func (r *Response) runProgram(run func()) {
	previous := r.order.inProgram
	r.order.inProgram = true
	defer func() { r.order.inProgram = previous }()
	run()
}
