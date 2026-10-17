package ui

import (
	"strings"
	"testing"
)

// toastNotice is the least a notice is drawn with: its words and a dismiss button with a name.
const toastNotice = `<card for="notice of notices; key: notice.id"><text>{{ notice.message }}</text><button click="dismiss(notice.id)"><icon value="x" alt="Đóng thông báo" /></button></card>`

// toastStack is a toast host with that notice; words are written on the host, after component=.
func toastStack(words string) string {
	return `<box component="toast"` + words + `>` + toastNotice + `</box>`
}

// toastSite is a site whose overlay.ui holds the toast written and whose content.ui is content.
func toastSite(overlay, content string) map[string]string {
	return map[string]string{
		"/site/page.ui":    `<page><content /><overlay /></page>`,
		"/site/overlay.ui": `<overlay>` + overlay + `</overlay>`,
		"/site/content.ui": content,
	}
}

// The toast the author draws (17/10/2026, docs/components/toast.md): the host is the stack — the classes
// of where it stands, a region named for a screen reader with the key that reaches it, tabindex -1, the
// alias, place and limit for the instance — and the one notice inside is the blueprint the kernel draws
// a row of for each notice: data-kitwork-for, written once, never a row painted by the server, each
// value that reads the notice bound (text, show, a link's address, an event), data-leaving bound to the
// notice's leaving. Last, the two regions a screen reader hears, each saying what is added to it alone
// (aria-atomic="false": a status or an alert says its whole region again by default). What the author writes on the host
// (gap, padding, width) and around the notice in it (the count waiting, a button that closes them all)
// stays theirs. A page in English names the region in English. CONTROL: alt names it, place and limit
// written go in as written.
func TestToastHostAndBlueprint(t *testing.T) {
	overlay := `<box component="toast" as="$toast" gap="2" padding="4">
  <card for="notice of notices; key: notice.id" flex="row" gap="3" padding="3">
    <icon show="notice.tone == 'danger'" value="alert-circle" color="danger" />
    <box gap="1">
      <text show="notice.title" weight="bold">{{ notice.title }}</text>
      <text>{{ notice.message }}</text>
      <button show="notice.action.click" click="act(notice.id)">{{ notice.action.label }}</button>
      <link show="notice.action.link" value="{{ notice.action.link }}">{{ notice.action.label }}</link>
    </box>
    <button click="dismiss(notice.id)"><icon value="x" alt="Đóng thông báo" /></button>
  </card>
  <text show="waiting > 0">{{ 'Còn ' + waiting }}</text>
  <button click="clear()">Đóng tất cả</button>
</box>`
	html := compileQuiet(t, toastSite(overlay, `<content><button click="$toast.danger('Không lưu được.')">Lưu</button></content>`))
	mustContain(t, html,
		`<div class="flex-column gap-2 padding-4 toast toast-bottom-end" data-kitwork-component="toast: $toast" role="region" aria-label="Thông báo (F8)" tabindex="-1" data-kitwork-scope="place: &#39;bottom-end&#39;; limit: 3">`,
		`<div class="card flex-row gap-3 padding-3" data-kitwork-for="notice of notices; key: notice.id" data-kitwork-bind="data-leaving: notice.leaving">`,
		`<i class="color-danger icon-alert-circle" aria-hidden="true" data-kitwork-bind="show: notice.tone == &#39;danger&#39;" hidden></i>`,
		`<p class="weight-bold" data-kitwork-bind="show: notice.title; text: notice.title" hidden></p>`,
		`<p data-kitwork-bind="text: notice.message"></p>`,
		`data-kitwork-bind="show: notice.action.click; text: notice.action.label" data-kitwork-event="click: act(notice.id)" hidden></button>`,
		`<a data-kitwork-bind="show: notice.action.link; href: notice.action.link; text: notice.action.label" hidden></a>`,
		`data-kitwork-event="click: dismiss(notice.id)"><i class="icon-x" role="img" aria-label="Đóng thông báo"></i></button></div>`,
		`<p data-kitwork-bind="show: waiting &gt; 0; text: &#39;Còn &#39; + waiting" hidden>Còn 0</p>`,
		`data-kitwork-event="click: clear()">Đóng tất cả</button><span class="reader" role="status" aria-atomic="false"></span><span class="reader" role="alert" aria-atomic="false"></span></div>`,
		`data-kitwork-event="click: $toast.danger(&#39;Không lưu được.&#39;)">Lưu</button>`,
	)
	// one blueprint, never a row painted: the server knows of no notice
	if n := strings.Count(html, "data-kitwork-for="); n != 1 {
		t.Errorf("the blueprint is written %d times, want once:\n%s", n, html)
	}
	mustNotContain(t, html, `role="status" aria-live`, `data-kit-item`)

	// the page's language names the region; alt names it; place and limit go in as written
	s := memorySite("", toastSite(toastStack(` as="$toast"`), `<content><text>x</text></content>`))
	s.Reload, s.Language = true, "en"
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, page.Render(Request{Path: "/"}), `aria-label="Notifications (F8)"`)
	html = compileQuiet(t, toastSite(toastStack(` as="$toast" place="top-start" limit="5" alt="Tin nhắn"`), `<content><text>x</text></content>`))
	mustContain(t, html, `class="flex-column toast toast-top-start" data-kitwork-component="toast: $toast" role="region" aria-label="Tin nhắn (F8)" tabindex="-1" data-kitwork-scope="place: &#39;top-start&#39;; limit: 5">`)
	// the toast's place is an edge of the screen: the overlay around it is no parent a child is placed
	// against (no place-anchor); a box placed below its parent makes one (the CONTROL)
	mustContain(t, html, `<div class="flex-column"><div class="flex-column toast toast-top-start"`)
	html = compileQuiet(t, toastSite(`<box><box place="below-start"><text>gợi ý</text></box></box>`+toastStack(` as="$toast"`), `<content><text>x</text></content>`))
	mustContain(t, html, `<div class="flex-column place-anchor">`)
}

// What the toast does not take is told where it is written, in the author's words: the empty host of
// before 17/10/2026 (and any host with no notice), a notice not right under the host, two, a for over
// another list, a key that is not the notice's id, no dismiss button, one with no name or one a show
// can hide (on it, on a frame around it, on the notice itself: a danger notice stays), a field the
// notice does not have, a component in a notice, a second toast, a host that is no box, if, for or
// show on the host, a place, a limit or persist it does not take, a blueprint that is no frame.
// CONTROL: each written right compiles (TestToastHostAndBlueprint).
func TestToastWrittenWrongIsAnError(t *testing.T) {
	dismiss := `<button click="dismiss(notice.id)"><icon value="x" alt="Đóng" /></button>`
	for _, c := range []struct{ name, overlay, needle, says string }{
		{"the empty host of before", `<box component="toast" as="$toast" />`, `<box`,
			`toast cần một mẫu thông báo bên trong thẻ chủ: <card for="notice of notices; key: notice.id">`},
		{"a host with no notice", `<box component="toast" as="$toast"><text>x</text></box>`, `<box`, "toast cần một mẫu thông báo"},
		{"a notice not right under the host", `<box component="toast" as="$toast"><box><card for="notice of notices; key: notice.id">` + dismiss + `</card></box></box>`, `for=`,
			"mẫu thông báo là con trực tiếp của thẻ chủ toast"},
		{"two notices", `<box component="toast" as="$toast">` + toastNotice + `<box for="n of notices; key: n.id"><button click="dismiss(n.id)"><icon value="x" alt="Đóng" /></button></box></box>`, `for="n of`,
			"toast có hai mẫu thông báo (overlay.ui:1:45 và overlay.ui:1:214): mỗi toast một mẫu"},
		{"the old list", `<box component="toast" as="$toast"><card for="item of items; key: item.id"><button click="dismiss(item.id)"><icon value="x" alt="Đóng" /></button></card></box>`, `for=`,
			`danh sách của toast là notices (items là tên cũ): viết for="item of notices; key: item.id"`},
		{"no key", `<box component="toast" as="$toast"><card for="notice of notices">` + dismiss + `</card></box>`, `for=`,
			"mẫu thông báo cần key: notice.id"},
		{"another key", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.message">` + dismiss + `</card></box>`, `for=`,
			"mẫu thông báo cần key: notice.id"},
		{"no dismiss button", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><text>{{ notice.message }}</text></card></box>`, `<card`,
			`mỗi thông báo cần một nút đóng: <button click="dismiss(notice.id)">`},
		{"a dismiss button with no name", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><button click="dismiss(notice.id)"><icon value="x" /></button></card></box>`, `<button`,
			"nút đóng của thông báo cần tên đọc được"},
		{"a dismiss button that hides", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><text>{{ notice.message }}</text><button show="notice.tone == 'nothing'" click="dismiss(notice.id)"><icon value="x" alt="Đóng" /></button></card></box>`, `show=`,
			"nút đóng của thông báo luôn hiện: bỏ show ở đây"},
		{"a dismiss button hidden from a width", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><text>{{ notice.message }}</text><button medium:show="false" click="dismiss(notice.id)"><icon value="x" alt="Đóng" /></button></card></box>`, `medium:show=`,
			"nút đóng của thông báo luôn hiện: bỏ medium:show ở đây"},
		{"a dismiss button in a box that hides", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><text>{{ notice.message }}</text><box show="notice.loading">` + dismiss + `</box></card></box>`, `show=`,
			"nút đóng của thông báo (overlay.ui:1:"},
		{"a notice that hides with its dismiss button", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id" show="notice.title"><text>{{ notice.message }}</text>` + dismiss + `</card></box>`, `show=`,
			"luôn hiện: bỏ show trên khung bọc nó"},
		{"a field it does not have", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><text>{{ notice.text }}</text>` + dismiss + `</card></box>`, `{{ notice.text`,
			"notice không có text: câu là notice.message"},
		{"a field near one", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><text>{{ notice.titel }}</text>` + dismiss + `</card></box>`, `{{ notice.titel`,
			"notice không có titel; một thông báo có id, message, title"},
		{"a field of the action it does not have", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><button show="notice.action.href" click="act(notice.id)">x</button>` + dismiss + `</card></box>`, `show=`,
			"notice.action không có href: viết notice.action.link"},
		{"a component in a notice", `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><box component="dropdown"><button click="toggle()">x</button></box>` + dismiss + `</card></box>`, `component="dropdown"`,
			"mẫu thông báo không chứa component: một phần tử, một component"},
		{"a blueprint that is no frame", `<box component="toast" as="$toast"><button for="notice of notices; key: notice.id" click="dismiss(notice.id)">x</button></box>`, `<button`,
			"mẫu thông báo là một khung"},
		{"a host that is no box", `<section component="toast" as="$toast">` + toastNotice + `</section>`, `component=`,
			"toast đặt trên <box>: chồng thông báo là một khung"},
		{"if on the host", toastStack(` as="$toast" if="ready"`), `if=`, "toast luôn có mặt và có một trên mỗi trang: bỏ if"},
		{"show on the host", toastStack(` as="$toast" show="ready"`), `show=`, "toast luôn có mặt và có một trên mỗi trang: bỏ show"},
		{"a place of a parent", toastStack(` as="$toast" place="below-end"`), `place=`,
			`place của toast là một mép hay góc màn hình: bottom-end, bottom, bottom-start, top-end, top, top-start; không phải "below-end"`},
		{"a place from data", toastStack(` as="$toast" place="{{ where }}"`), `place=`, "place của toast là chữ cố định, không có {{ }}"},
		{"a place by width", toastStack(` as="$toast" medium:place="top"`), `medium:place=`, "place của toast không nhận tiền tố"},
		{"a limit past ten", toastStack(` as="$toast" limit="12"`), `limit=`, `limit là số thông báo hiện cùng lúc, từ 1 đến 10 (mặc định 3), không phải "12"`},
		{"persist", toastStack(` as="$toast" persist="true"`), `persist=`, "toast không giữ gì qua các lần ghé"},
		{"a role", toastStack(` as="$toast" role="status"`), `role=`, `vai trò của toast là việc của engine (role="region"`},
		{"a second toast", toastStack(` as="$toast"`) + toastStack(` as="$other"`), `component="toast" as="$other`,
			"trang đã có một toast ở overlay.ui:1:15: mỗi trang một chồng thông báo"},
	} {
		t.Run(c.name, func(t *testing.T) {
			at := where("overlay.ui", `<overlay>`+c.overlay+`</overlay>`, c.needle)
			mustFail(t, toastSite(c.overlay, `<content><text>x</text></content>`), at, c.says)
		})
	}
	// a toast written as a tag is a component: written on a <box>, with its notice inside
	mustFail(t, content(`<content><toast as="$toast" /></content>`), "content.ui:1:10: ", `toast có logic (component của Kitwork): viết <box component="toast" as="$toast">…</box>`)
}

// What works and is probably not meant is a warning, and the page renders: a toast with no as (nothing
// can say a notice), a blueprint that never shows notice.message (notices without words), and a toast
// inside a lazy frame (its fixed stack placed by the frame, not the screen). CONTROL: the toast
// written right warns of nothing (TestToastHostAndBlueprint compiles it quietly).
func TestToastWarnings(t *testing.T) {
	stack := toastStack(``)
	compileWarns(t, toastSite(stack, `<content><text>x</text></content>`), where("overlay.ui", `<overlay>`+stack+`</overlay>`, `component=`),
		`toast không có as: trang không gọi được show; viết as="$toast"`)
	silent := `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id"><text>{{ notice.title }}</text><button click="dismiss(notice.id)"><icon value="x" alt="Đóng" /></button></card></box>`
	compileWarns(t, toastSite(silent, `<content><text>x</text></content>`), where("overlay.ui", `<overlay>`+silent+`</overlay>`, `<card`),
		"mẫu thông báo không hiện notice.message: thông báo sẽ không có chữ")
	lazy := `<content><section lazy="600px">` + toastStack(` as="$toast"`) + `</section></content>`
	compileWarns(t, content(lazy), strings.TrimSuffix(where("content.ui", lazy, `lazy=`), " "), `lazy="600px": khung này giữ <box component="toast"> ở content.ui:1:`)
}

// Each call through the toast's alias is read once the page is compiled (checkToastCalls): a function
// the toast has, with its own words (error is danger, hide is the template pages', close is dismiss), a
// message, options it reads (a key of another library names the toast's; a tone of the palette; a
// tone beside a function that is one; a duration with its unit, up to 60s; an action with a label and
// a click that is an arrow function of nothing, or a link the link rule takes; an id that is a name
// starting with a letter: a number is the toast's own, and one the author gave would meet it),
// what the toast's state is (message and visible are the template pages'); and a notice the blueprint
// cannot draw: a button or a link it has no place for. Warnings: a title it does not show, a tone it
// does not change with, a duration too short to read or to reach its button. CONTROL: the calls of the
// design, against a blueprint that draws it all, compile quietly.
func TestToastCallsAreRead(t *testing.T) {
	full := `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id">` +
		`<icon show="notice.tone == 'danger'" value="alert-circle" color="danger" />` +
		`<text show="notice.title" weight="bold">{{ notice.title }}</text><text>{{ notice.message }}</text>` +
		`<button show="notice.action.click" click="act(notice.id)">{{ notice.action.label }}</button>` +
		`<link show="notice.action.link" value="{{ notice.action.link }}">{{ notice.action.label }}</link>` +
		`<button click="dismiss(notice.id)"><icon value="x" alt="Đóng" /></button></card></box>`
	call := func(click string) string {
		return `<content><box component="tabs" as="$files"><button click="select('a')">a</button></box><box show="$files.selected == 'a'"><text>a</text></box><button click="` + click + `">x</button></content>`
	}
	for _, good := range []string{
		`$toast.success('Đã lưu.')`,
		`$toast.danger('Không lưu được. Kiểm tra kết nối rồi thử lại.')`,
		`$toast.show('Đã xoá ghi chú.', { title: 'Ghi chú', action: { label: 'Hoàn tác', click: () => $files.select('a') } })`,
		`$toast.loading('Đang tải ảnh lên…', { id: 'upload' })`,
		`$toast.update('upload', 'Đã tải ảnh lên.', { tone: 'success' })`,
		`$toast.info('Ở lại tới khi đóng.', { duration: 0 })`,
		`$toast.show('Xem giỏ.', { action: { label: 'Giỏ', link: '/gio-hang' }, duration: '10s' })`,
		`$toast.warning('Sắp hết giờ.', { duration: '2.5s', id: 'gio-2' })`,
		`$toast.update('upload', 'Lại.', { title: '', action: null })`,
		`$toast.dismiss('upload')`,
		`$toast.clear()`,
	} {
		compileQuiet(t, toastSite(full, call(good)))
	}
	for _, c := range []struct{ click, says string }{
		{`$toast.error('x')`, "toast không có error: màu của bảng là danger, viết $toast.danger(…)"},
		{`$toast.hide()`, "hide là chữ của trang template: viết $toast.clear() (đóng mọi thông báo) hay $toast.dismiss(id) (đóng một)"},
		{`$toast.close('a')`, "toast không có close: viết $toast.dismiss(id)"},
		{`$toast.sucess('x')`, "toast không có sucess; hàm của toast là show, info, success"},
		{`$toast.show('')`, "$toast.show(''): thông báo cần một câu"},
		{`$toast.show()`, "$toast.show cần câu thông báo"},
		{`$toast.show('x', { type: 'success' })`, "options của toast không có type: viết tone"},
		{`$toast.show('x', { timeout: 3000 })`, "options của toast không có timeout: viết duration"},
		{`$toast.show('x', { description: 'y' })`, "options không có description: câu là đối số đầu"},
		{`$toast.show('x', { colour: 'y' })`, "options của toast không có colour; options có tone, title, duration, action, id, loading"},
		{`$toast.show('x', { tone: 'error' })`, "tone: 'error' không có: màu của bảng là danger"},
		{`$toast.show('x', { tone: 'primary' })`, `tone của thông báo là info, success, warning, danger, không phải "primary"`},
		{`$toast.success('x', { tone: 'danger' })`, "success đã là tone; bỏ tone, hay gọi $toast.danger(…)"},
		{`$toast.show('x', { duration: 3000 })`, "duration: 3000 thiếu đơn vị: viết duration: '3000ms'"},
		{`$toast.show('x', { duration: 3 })`, "duration: 3 thiếu đơn vị: viết duration: '3s'"},
		{`$toast.show('x', { duration: '3 s' })`, "duration: '3 s' không phải một thời lượng"},
		{`$toast.show('x', { duration: -3 })`, "duration không âm"},
		{`$toast.show('x', { duration: '90s' })`, "dài hơn 60 giây: thông báo ở lại tới khi đóng thì viết duration: 0"},
		{`$toast.show('x', { action: { click: () => $toast.clear() } })`, "action cần label"},
		{`$toast.show('x', { action: { label: 'L' } })`, "action cần click (một hàm mũi tên"},
		{`$toast.show('x', { action: { label: 'L', click: () => $toast.clear(), link: '/a' } })`, "action có cả click và link"},
		{`$toast.show('x', { action: { label: 'L', click: 'restore' } })`, "click của action là một hàm mũi tên viết ngay tại chỗ"},
		{`$toast.show('x', { action: { label: 'L', click: (n) => $toast.clear() } })`, "click của action không nhận tham số"},
		{`$toast.show('x', { action: { label: 'L', href: '/a' } })`, "action không có href: viết link"},
		{`$toast.show('x', { action: { label: 'L', link: 'javascript:alert(1)' } })`, "link của action chỉ đi tới đường dẫn trong site"},
		{`$toast.show('x', { id: 'hai từ' })`, `id của thông báo là một tên bắt đầu bằng chữ cái (chữ, số, - và _), không phải "hai từ"`},
		{`$toast.show('x', { id: 7 })`, "id của thông báo là một tên bắt đầu bằng chữ cái (chữ, số, - và _): số là của toast (1, 2, 3…), một số người viết đặt sẽ trùng nó"},
		{`$toast.show('x', { id: '7' })`, `id của thông báo là một tên bắt đầu bằng chữ cái (chữ, số, - và _), không phải "7"`},
		{`$toast.update('x')`, "$toast.update nhận id và câu mới"},
		{`$toast.message`, "$toast.message là chữ của trang template: thông báo đang hiện là $toast.notices"},
	} {
		source := toastSite(full, call(c.click))
		mustFail(t, source, where("content.ui", source["/site/content.ui"], `click="$toast`), c.says)
	}

	// a notice the blueprint cannot draw: an error at the call, naming the blueprint and what to write
	bare := toastStack(` as="$toast"`)
	blueprint := strings.TrimSuffix(where("overlay.ui", `<overlay>`+bare+`</overlay>`, `<card`), ": ")
	for _, c := range []struct{ click, says string }{
		{`$toast.show('x', { action: { label: 'L', click: () => $toast.clear() } })`,
			`thông báo có nút (action.click) mà mẫu thông báo (` + blueprint + `) không có <button click="act(notice.id)">`},
		{`$toast.show('x', { action: { label: 'L', link: '/a' } })`,
			`thông báo có link (action.link) mà mẫu thông báo (` + blueprint + `) không có <link value="{{ notice.action.link }}">`},
	} {
		source := toastSite(bare, call(c.click))
		mustFail(t, source, where("content.ui", source["/site/content.ui"], `click="$toast`), c.says)
	}
	// what it would not show, or not in time: warnings, the page renders
	for _, c := range []struct{ overlay, click, says string }{
		{bare, `$toast.show('x', { title: 'T' })`, "thông báo có title mà mẫu thông báo (" + blueprint + ") không đọc notice.title"},
		{full, `$toast.show('x', { duration: '1s' })`, "duration dưới 2 giây: người xem đọc không kịp"},
		{full, `$toast.show('x', { duration: '4s', action: { label: 'L', click: () => $toast.clear() } })`, "thông báo có nút mà duration dưới 8 giây"},
	} {
		source := toastSite(c.overlay, call(c.click))
		compileWarns(t, source, where("content.ui", source["/site/content.ui"], `click="$toast`), c.says)
	}
	source := toastSite(bare, call(`$toast.danger('x')`))
	compileWarns(t, source, where("overlay.ui", source["/site/overlay.ui"], `<card`), "mẫu thông báo không đổi theo notice.tone, mà content.ui:1:")
}

// A toast inside a glass frame is placed by the frame (its backdrop-filter holds what is fixed inside
// it), not by the screen: a warning at the toast, naming the material. CONTROL: the same frame with
// material="none", and the toast beside a glass frame, warn of nothing.
func TestToastInsideGlassIsAWarning(t *testing.T) {
	glass := `<content><section material="glass">` + toastStack(` as="$toast"`) + `</section></content>`
	compileWarns(t, content(glass), where("content.ui", glass, `component=`),
		"toast trong khung kính (material ở content.ui:1:19) bám theo khung đó, không theo màn hình")
	compileQuiet(t, content(`<content><section material="none">`+toastStack(` as="$toast"`)+`</section></content>`))
	compileQuiet(t, content(`<content><section material="glass"><text>x</text></section>`+toastStack(` as="$toast"`)+`</content>`))
}
