package ui

import (
	"regexp"
	"strings"
	"testing"
)

// networkNotice is the notice of the kitwork example's overlay.ui, cut to what these tests read: an
// icon for each state, the words, and the button that closes it.
const networkNotice = `<card show="open" flex="row" gap="3" padding="3">` +
	`<icon show="!connected" value="wifi-off" color="warning+5" alt="Offline" />` +
	`<icon show="connected" value="wifi" color="success" alt="Online" />` +
	`<text weight="bold">{{ connected ? 'Back online' : 'You are offline' }}</text>` +
	`<button click="close()"><icon value="x" alt="Dismiss" /></button></card>`

// networkWithNotice is a network host with that notice; words are written on the host, after component=.
func networkWithNotice(words string) string {
	return `<box component="network"` + words + `>` + networkNotice + `</box>`
}

// networkToast is a toast whose notice changes with its tone, so a page that says a warning through it
// is told nothing (TestToastCallsAreRead).
const networkToast = `<box component="toast" as="$toast"><card for="notice of notices; key: notice.id">` +
	`<icon show="notice.tone == 'warning'" value="alert-triangle" color="warning+5" alt="Warning" />` +
	`<text>{{ notice.message }}</text>` +
	`<button click="dismiss(notice.id)"><icon value="x" alt="Dismiss" /></button></card></box>`

// The network the author draws (17/10/2026): placed, the host floats at its edge (classes network and
// network-<place>, reboot.css) and is a status region, since what shows in it and what it changes to is
// for a screen reader to say; the alias names the instance. Inside, the component's names are read:
// the card shows while open, hidden on the server's paint (open starts false); the icons and the words
// follow connected, painted as the server knows it (true: the wifi icon, "Back online"); close() is
// the button's click. Elsewhere the page reads the instance through its alias, as it starts. The
// toast beside it in the overlay and the network are no parents a child is placed against: the overlay
// has no place-anchor. CONTROL: a box placed below its parent makes one.
func TestNetworkHostWithItsNotice(t *testing.T) {
	overlay := networkToast + networkWithNotice(` as="$network" place="bottom-start"`)
	html := compileQuiet(t, toastSite(overlay, `<content><text show="!$network.connected">Offline</text><text>{{ $network.open }}</text></content>`))
	mustContain(t, html,
		`<div class="flex-column network network-bottom-start" data-kitwork-component="network: $network" role="status">`,
		`<div class="card flex-row gap-3 padding-3" data-kitwork-bind="show: open" hidden>`,
		`<i class="color-warning+5 icon-wifi-off" role="img" aria-label="Offline" data-kitwork-bind="show: !connected" hidden></i>`,
		`<i class="color-success icon-wifi" role="img" aria-label="Online" data-kitwork-bind="show: connected"></i>`,
		`<p class="weight-bold" data-kitwork-bind="text: connected ? &#39;Back online&#39; : &#39;You are offline&#39;">Back online</p>`,
		`data-kitwork-event="click: close()"><i class="icon-x" role="img" aria-label="Dismiss"></i></button></div></div>`,
		`<p data-kitwork-bind="show: !$network.connected" hidden>Offline</p>`,
		`<p data-kitwork-bind="text: $network.open">false</p>`,
		`</main><div class="flex-column"><div class="flex-column toast toast-bottom-end"`,
	)
	// one status region the engine writes on the host, beside the toast's own (a reader span)
	if n := strings.Count(html, `data-kitwork-component="network: $network" role="status"`); n != 1 {
		t.Errorf("the host is a status region %d times, want once:\n%s", n, html)
	}
	mustNotContain(t, html, `aria-live`, `place-anchor`)
	// CONTROL: a box placed below its parent in the same overlay makes its parent an anchor
	html = compileQuiet(t, toastSite(`<box><box place="below-start"><text>hint</text></box></box>`+overlay, `<content><text>x</text></content>`))
	mustContain(t, html, `<div class="flex-column place-anchor"><div class="flex-column place-below-start`)
	mustContain(t, html, `</div><div class="flex-column network network-bottom-start" data-kitwork-component="network: $network" role="status">`)
}

// Without place the host stays where it is written, a line of the form it is in: no class of where it
// floats, and still a status region, since it has something inside. Each of the six places is a class
// of its own.
func TestNetworkWithoutPlaceStaysInTheFlow(t *testing.T) {
	html := compileQuiet(t, content(`<content><box gap="2"><box component="network" as="$network"><text show="!connected">Changes wait for the connection.</text></box><text>Form</text></box></content>`))
	mustContain(t, html, `<div class="flex-column gap-2"><div class="flex-column" data-kitwork-component="network: $network" role="status"><p data-kitwork-bind="show: !connected" hidden>Changes wait for the connection.</p></div>`)
	mustNotContain(t, html, `network-`, `class="flex-column network`)
	for _, place := range toastPlaces {
		html := compileQuiet(t, toastSite(networkWithNotice(` place="`+place+`"`), `<content><text>x</text></content>`))
		mustContain(t, html, `<div class="flex-column network network-`+place+`" data-kitwork-component="network" role="status">`)
	}
}

// Written empty, the host is heard and never seen: its events are wired (each written once, with its
// modifiers), it is hidden by the engine (show-false: it takes no room in the page's flow), and it is no
// status region, since nothing in it would be said. A show the author writes is theirs: the engine adds
// none. Its alias still reads the instance.
func TestNetworkEmptyHostIsHeardNotSeen(t *testing.T) {
	empty := `<box component="network" as="$network" offline="$toast.warning('You are offline.')" online="$toast.info('Back online.')" />`
	html := compileQuiet(t, toastSite(networkToast+empty, `<content><text>{{ $network.connected ? 'Online' : 'Offline' }}</text></content>`))
	mustContain(t, html,
		`<div class="flex-column show-false" data-kitwork-component="network: $network" data-kitwork-event="offline: $toast.warning(&#39;You are offline.&#39;); online: $toast.info(&#39;Back online.&#39;)"></div>`,
		`<p data-kitwork-bind="text: $network.connected ? &#39;Online&#39; : &#39;Offline&#39;">Online</p>`)
	// hidden by the page's own stylesheet, as any show="false" (the kernel still starts it by its name)
	s := memorySite("", toastSite(networkToast+empty, `<content><text>x</text></content>`))
	s.Reload = true
	if _, css := renderWithCSS(t, s, ".", "/", nil); !strings.Contains(css, ".show-false{display:none}") {
		t.Errorf("the page's stylesheet does not hide the empty host:\n%s", css)
	}

	// the show the author writes stays the only one, with a width too (shown from medium, never hidden
	// below it by the engine)
	for _, c := range []struct{ show, class string }{{` show="true"`, `flex-column`}, {` medium:show="true"`, `flex-column medium:show-true`}} {
		shown := `<box component="network" as="$network"` + c.show + ` offline="$toast.warning('You are offline.')" />`
		html = compileQuiet(t, toastSite(networkToast+shown, `<content><text>x</text></content>`))
		mustContain(t, html, `<div class="`+c.class+`" data-kitwork-component="network: $network" data-kitwork-event="offline: $toast.warning(&#39;You are offline.&#39;)"></div>`)
		mustNotContain(t, html, `show-false`)
	}

	// once is a modifier of any event: said once, then no more
	once := `<box component="network" offline:once="$toast.warning('You are offline.')" />`
	html = compileQuiet(t, toastSite(networkToast+once, `<content><text>x</text></content>`))
	mustContain(t, html, `<div class="flex-column show-false" data-kitwork-component="network" data-kitwork-event="offline.once: $toast.warning(&#39;You are offline.&#39;)"></div>`)
}

// What the network does not take is told where it is written: its place by width, from data, empty or
// not an edge of the screen; a role it writes itself; online and offline anywhere but on the host of
// Kitwork's network (they would wait for an event nothing sends there): a box, a button, a text, a
// child of the network's host, the host of another of Kitwork's components, a site's own component
// that is named network, an event listed with click; a key filter on an event that is no key; the
// web's on-names; a host it does not take; the tag form. And the empty host stays the network's own:
// dropdown, a component without a UI too, is nothing written empty.
// CONTROL: each written right compiles (the tests above).
func TestNetworkWrittenWrongIsAnError(t *testing.T) {
	warn := `$toast.warning('x')`
	for _, c := range []struct{ name, content, needle, says string }{
		{"a place by width", networkWithNotice(` medium:place="top"`), `medium:place=`, "place của network không nhận tiền tố"},
		{"a place from data", networkWithNotice(` place="{{ where }}"`), `place=`, "place của network là chữ cố định, không có {{ }}"},
		{"an empty place", networkWithNotice(` place=""`), `place=`,
			"place của network cần một giá trị: bottom-end, bottom, bottom-start, top-end, top, top-start"},
		{"a bare place", networkWithNotice(` place`), `place>`,
			"place của network cần một giá trị: bottom-end, bottom, bottom-start, top-end, top, top-start"},
		{"a place of a parent", networkWithNotice(` place="below-end"`), `place=`,
			`place của network là một mép hay góc màn hình: bottom-end, bottom, bottom-start, top-end, top, top-start; không phải "below-end"`},
		{"a role", networkWithNotice(` role="alert"`), `role=`, `vai trò của network là việc của engine (role="status" khi bên trong có thông báo): bỏ role`},
		{"aria-live", networkWithNotice(` aria-live="assertive"`), `aria-live=`, "vai trò của network là việc của engine (role=\"status\" khi bên trong có thông báo): bỏ aria-live"},
		{"aria-atomic", networkWithNotice(` aria-atomic="true"`), `aria-atomic=`, "bỏ aria-atomic"},

		{"offline on a box", `<box offline="` + warn + `"><text>a</text></box>`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" offline="…" />`},
		{"online on a button", `<button online="` + warn + `">a</button>`, `online=`,
			`online là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" online="…" />`},
		{"offline on a text", `<text offline="` + warn + `">a</text>`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được`},
		{"offline on a link", `<link value="/a" offline="` + warn + `">a</link>`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được`},
		{"offline on a child of the host", `<box component="network"><box offline="` + warn + `"><text>a</text></box></box>`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được`},
		{"offline on a text in the host", `<box component="network"><text offline="` + warn + `">a</text></box>`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được`},
		{"offline on the host of dropdown", `<box component="dropdown" offline="close()"><button click="toggle()">a</button></box>`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được`},
		{"offline listed with click", `<button click,offline="` + warn + `">a</button>`, `click,offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" offline="…" />`},
		{"online with its modifier", `<box online:once="` + warn + `"><text>a</text></box>`, `online:once=`,
			`<box component="network" online:once="…" />`},
		{"a key filter on offline", `<box component="network" offline:escape="` + warn + `" />`, `offline:escape=`,
			"offline:escape: escape và enter lọc một phím, đặt trên keydown hoặc keyup"},
		{"the web's ononline", `<box ononline="` + warn + `"><text>a</text></box>`, `ononline=`,
			`ononline là chữ của web; sự kiện viết bằng tên của nó, không có on: online="` + warn + `"`},
		{"the web's onoffline on the host", `<box component="network" onoffline="` + warn + `" />`, `onoffline=`,
			`onoffline là chữ của web; sự kiện viết bằng tên của nó, không có on: offline="` + warn + `"`},
		{"a host it does not take", `<button component="network">a</button>`, `component=`,
			"network đặt trên <box>, <card>, <item>, <section>, không trên <button>"},
		{"the tag form", `<network offline="` + warn + `" />`, `<network`,
			`network có logic (component của Kitwork): viết <box component="network" …>…</box>`},
		{"dropdown written empty", `<box component="dropdown" />`, `<box`,
			`dropdown không có giao diện: nội dung bên trong thẻ là giao diện của nó (<box component="dropdown">…</box>)`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + c.content + `</content>`
			mustFail(t, toastSite(networkToast, source), where("content.ui", source, c.needle), c.says)
		})
	}

	// written in a site of their own: the toast's host, the page's one toast (its overlay holds none
	// here); a layout called by its tag, which takes no event (its own words, before the network's); a
	// component of the site named network, the site's and not Kitwork's (it sends neither event); and a
	// key filter on an event that is no key, told the same way on any event (click: the CONTROL)
	mine := `import { component } from "kitwork";

component.define("network", () => {
  var connected = true;
  const close = () => {};
});
`
	for _, c := range []struct {
		name, content, needle, says string
		files                       map[string]string // added to the content alone
	}{
		{"offline on the host of the toast", strings.Replace(networkToast, `as="$toast"`, `as="$toast" offline="clear()"`, 1), `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" offline="…" />`, nil},
		{"offline on a layout called by its tag", `<offline-line offline="close()" />`, `offline=`,
			"sự kiện offline đặt trong offline-line.ui: offline-line là layout",
			map[string]string{"/site/offline-line.ui": `<box><text>Offline</text></box>`}},
		{"offline on the site's own network", `<box component="network" offline="close()"><text>{{ connected }}</text></box>`, `offline=`,
			"offline là sự kiện của network của Kitwork; network ở đây là component của site (network.work), không gửi sự kiện đó: đặt tên khác cho component này",
			map[string]string{"/site/network.work": mine}},
		{"a key filter on click", `<button click:escape="clear()">a</button>`, `click:escape=`,
			"click:escape: escape và enter lọc một phím, đặt trên keydown hoặc keyup", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + c.content + `</content>`
			files := content(source)
			for name, file := range c.files {
				files[name] = file
			}
			mustFail(t, files, where("content.ui", source, c.needle), c.says)
		})
	}
}

// What works and is probably not meant is a warning, and the page renders: a place on an empty host
// (it is never seen, so the place places nothing), a placed network inside a glass frame or a lazy one
// (each holds what is fixed inside it by itself, not by the screen, as for the toast). CONTROL: the
// network without place in either frame, and in a frame of material none, warns of nothing.
func TestNetworkWarnings(t *testing.T) {
	empty := `<box component="network" place="bottom-start" offline="$toast.warning('You are offline.')" />`
	html := compileWarns(t, toastSite(networkToast+empty, `<content><text>x</text></content>`),
		where("overlay.ui", `<overlay>`+networkToast+empty+`</overlay>`, `place=`),
		`place="bottom-start" trên network rỗng: thẻ chủ rỗng chỉ nghe sự kiện và không hiện, place không đặt gì`)
	mustContain(t, html, `<div class="flex-column show-false" data-kitwork-component="network" data-kitwork-event="offline: $toast.warning(&#39;You are offline.&#39;)"></div>`)

	glass := `<content><section material="glass">` + networkWithNotice(` place="top"`) + `</section></content>`
	compileWarns(t, content(glass), where("content.ui", glass, `component=`),
		"network có place trong khung kính (material ở content.ui:1:19) bám theo khung đó, không theo màn hình")
	lazy := `<content><section lazy="600px">` + networkWithNotice(` place="top"`) + `</section></content>`
	compileWarns(t, content(lazy), strings.TrimSuffix(where("content.ui", lazy, `lazy=`), " "),
		`lazy="600px": khung này giữ <box component="network" place="top"> ở content.ui:1:`)

	// CONTROL: in the page's flow a frame places it where it is written, as it places anything
	compileQuiet(t, content(`<content><section material="glass">`+networkWithNotice(``)+`</section></content>`))
	compileQuiet(t, content(`<content><section lazy="600px">`+networkWithNotice(``)+`</section></content>`))
	compileQuiet(t, content(`<content><section material="none">`+networkWithNotice(` place="top"`)+`</section></content>`))
}

// offline and online are refused off the network's host also on the tags that compile their own
// attributes, each naming the network and the host to write (componentEventsHere runs in each): a
// quote, an accordion, its header (the first child, compiled into <summary>), a breadcrumb, a code, and
// a code inside a terminal (a command). Until 17/10/2026 these six compiled without a word and dropped
// the handler. CONTROL: each written without the event compiles quietly.
func TestNetworkEventsOnTagsOfTheirOwn(t *testing.T) {
	warn := `$toast.warning('x')`
	for _, c := range []struct{ name, markup, event, needle, says string }{
		{"a quote", `<quote EVENT><text>a</text></quote>`, ` offline="` + warn + `"`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" offline="…" />`},
		{"an accordion", `<accordion EVENT><text click="toggle()">Q</text><text>A</text></accordion>`, ` online="` + warn + `"`, `online=`,
			`online là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" online="…" />`},
		{"the header of an accordion", `<accordion><text click="toggle()" EVENT>Q</text><text>A</text></accordion>`, ` offline="` + warn + `"`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" offline="…" />`},
		{"a breadcrumb", `<breadcrumb EVENT><item><link value="/">Home</link></item></breadcrumb>`, ` online="` + warn + `"`, `online=`,
			`online là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" online="…" />`},
		{"a code", `<code EVENT>ls</code>`, ` offline="` + warn + `"`, `offline=`,
			`offline là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" offline="…" />`},
		{"a command of a terminal", `<box component="terminal"><code EVENT>ls</code></box>`, ` online:once="` + warn + `"`, `online:once=`,
			`online là sự kiện của component network, chỉ thẻ chủ của nó nghe được: <box component="network" online:once="…" />`},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + strings.Replace(c.markup, " EVENT", c.event, 1) + `</content>`
			mustFail(t, toastSite(networkToast, source), where("content.ui", source, c.needle), c.says)
			// CONTROL: the same tag without the event
			compileQuiet(t, toastSite(networkToast, `<content>`+strings.Replace(c.markup, " EVENT", "", 1)+`</content>`))
		})
	}
}

// On an <item> the host keeps its role in its list: a live region by the two words role="status" means
// (aria-live="polite" aria-atomic="true"), never role="status", which would take the item out of the
// list; the list stays a list. Written empty it is no live region at all (nothing in it is said).
// CONTROL: the same host on a box is a status region.
func TestNetworkOnAnItemKeepsItsRole(t *testing.T) {
	html := compileQuiet(t, content(`<content><list><item component="network" as="$network"><text show="!connected">Offline</text></item><item><text>Form</text></item></list></content>`))
	mustContain(t, html, `<ul class="flex-column" role="list"><li class="flex-column" data-kitwork-component="network: $network" aria-live="polite" aria-atomic="true"><p data-kitwork-bind="show: !connected" hidden>Offline</p></li><li class="flex-column"><p>Form</p></li></ul>`)
	mustNotContain(t, html, `role="status"`, `role="listitem"`)

	html = compileQuiet(t, content(`<content><list><item component="network" as="$network" /><item><text>{{ $network.connected }}</text></item></list></content>`))
	mustContain(t, html, `<ul class="flex-column" role="list"><li class="flex-column show-false" data-kitwork-component="network: $network"></li>`)
	mustNotContain(t, html, `aria-live`, `role="status"`)

	// CONTROL: on a box
	html = compileQuiet(t, content(`<content><box component="network" as="$network"><text show="!connected">Offline</text></box></content>`))
	mustContain(t, html, `<div class="flex-column" data-kitwork-component="network: $network" role="status">`)
	mustNotContain(t, html, `aria-live`)
}

// A region is no host of the network: it keeps its own role (a landmark a screen reader lists), and a
// placed one would pin the region itself to the screen. Told where component= is written, with the
// tags it is written on instead: the root of sidebar.ui, a footer written in page.ui with place, and
// every other region in a file of its own. CONTROL: the same network on a box inside the region
// compiles, a status region inside the landmark.
func TestNetworkIsNoRegion(t *testing.T) {
	const notice = `<text show="!connected">Offline</text>`
	const says = "đặt nó trên một thẻ bên trong (<box>, <card>, <item>, <section>)"
	sidebar := `<sidebar component="network">` + notice + `</sidebar>`
	mustFail(t, map[string]string{
		"/site/page.ui":    `<page><sidebar /><content /></page>`,
		"/site/sidebar.ui": sidebar,
		"/site/content.ui": `<content><text>x</text></content>`,
	}, where("sidebar.ui", sidebar, `component=`), "network không đặt trên vùng <sidebar>: "+says)

	page := `<page><content /><footer component="network" place="bottom">` + notice + `</footer></page>`
	mustFail(t, map[string]string{
		"/site/page.ui":    page,
		"/site/content.ui": `<content><text>x</text></content>`,
	}, where("page.ui", page, `component=`), "network không đặt trên vùng <footer>: "+says)

	for _, region := range []string{"header", "navbar", "tabbar", "overlay"} {
		t.Run(region, func(t *testing.T) {
			source := `<` + region + ` component="network">` + notice + `</` + region + `>`
			mustFail(t, map[string]string{
				"/site/page.ui":           `<page><` + region + ` /><content /></page>`,
				"/site/" + region + ".ui": source,
				"/site/content.ui":        `<content><text>x</text></content>`,
			}, where(region+".ui", source, `component=`), "network không đặt trên vùng <"+region+">: "+says)
		})
	}

	// CONTROL: inside the region, on a box
	html := compileQuiet(t, map[string]string{
		"/site/page.ui":    `<page><sidebar /><content /></page>`,
		"/site/sidebar.ui": `<sidebar><box component="network">` + notice + `</box></sidebar>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	})
	mustContain(t, html, `<aside class="flex-column"><div class="flex-column" data-kitwork-component="network" role="status">`)
	html = compileQuiet(t, map[string]string{
		"/site/page.ui":    `<page><content /><footer><box component="network" place="bottom">` + notice + `</box></footer></page>`,
		"/site/content.ui": `<content><text>x</text></content>`,
	})
	mustContain(t, html, `<footer class="flex-column"><div class="flex-column network network-bottom" data-kitwork-component="network" role="status">`)
}

// Placed, the host floats over whatever the page holds there, at every width: a notice on a
// translucent background (ink/10) is measured over that content as the contrast check knows it, white
// and black, as a dropdown's popover is, and the ink of the page's text is short on it. CONTROL: the
// same notice in the page's flow stands on the canvas and passes.
func TestNetworkPlacedNoticeFloatsOverThePage(t *testing.T) {
	notice := `<card background="ink/10"><text>You are offline</text></card>`
	contrastFails(t, nil, content(`<content><box component="network" place="bottom-start">`+notice+`</box></content>`), `background=`,
		"chữ ink mặc định của trang trên nền ink/10 phủ trên nội dung bên dưới (xét cả nền trắng và nền đen) chỉ 1.32:1 ở chế độ sáng; chữ thường cần 4.5:1 (chữ lớn 3:1). "+
			"Không màu chữ nào, kể cả trắng hay đen, đọc được trên nền này: đổi nền ink (background, material) hay khai lại ink trong router.colors.")
	// CONTROL: unplaced, the notice is a line of the page, on the canvas
	contrastPasses(t, nil, content(`<content><box component="network">`+notice+`</box></content>`))
}

// The place of Kitwork's network (and toast) is an edge of the screen, not a place against the parent,
// so the box around it is no place-anchor. A site's own component named network or toast is the site's:
// its place="below-start" places it against its parent like any other, and the parent is an anchor.
func TestNetworkPlaceAnchorIsForPlacesAgainstTheParent(t *testing.T) {
	html := compileQuiet(t, content(`<content><box><box component="network" place="bottom-start"><text>{{ connected }}</text></box></box></content>`))
	mustContain(t, html, `<main class="flex-column"><div class="flex-column"><div class="flex-column network network-bottom-start" data-kitwork-component="network" role="status">`)
	mustNotContain(t, html, `place-anchor`)

	for _, name := range []string{"network", "toast"} {
		t.Run("the site's "+name, func(t *testing.T) {
			files := content(`<content><box><box component="` + name + `" place="below-start"><text>{{ count }}</text></box></box></content>`)
			files["/site/"+name+".work"] = "import { component } from \"kitwork\";\n\ncomponent.define(\"" + name + "\", () => {\n  var count = 1;\n});\n"
			html := compileQuiet(t, files)
			mustContain(t, html, `<main class="flex-column"><div class="flex-column place-anchor"><div class="flex-column place-below-start"><p>1</p></div></div></main>`)
		})
	}
}

// networkHostRe is the network's host as a page renders it: its opening tag, every attribute in it.
var networkHostRe = regexp.MustCompile(`<[a-z]+ [^>]*data-kitwork-component="network[^"]*"[^>]*>`)

// networkCompile compiles one content.ui that must compile (reloading, so no progressbar is added) and
// returns the network's host as rendered at "/" with no data, the whole page, and the warnings of the
// compile, one line each (file:line:col: message).
func networkCompile(t *testing.T, source string) (host, html, warned string) {
	t.Helper()
	s := memorySite("", content(source))
	s.Reload = true
	page, lines := compileDiagnose(t, s)
	html = page.Render(Request{Path: "/"})
	return networkHostRe.FindString(html), html, strings.Join(lines, "\n")
}

// delay and duration (17/10/2026) reach the instance as written, in milliseconds, for the kernel to seed
// it with before its init (data-kitwork-scope): only the ones written, delay first, since the
// component's own (1s, 4s) live in the component. Both, either one, the two zeros (delay 0 says it at
// once, duration 0 stays until closed; written bare or with a unit), either order, spaces around, the
// seconds read as the decimal written (4.03s is 4030, never 4030.0000000000005), each limit itself
// (10s, 60s, the 2s a notice reads in) and a short delay (a delay is no notice to read) all say nothing.
// Written empty, the host takes delay (its events wait for it) and nothing else. A host that writes
// neither word writes no data-kitwork-scope at all: in the flow, placed, or empty.
// CONTROL: inputs() returning nothing, or skipping a zero, leaves the scope out (run by hand, 17/10/2026).
func TestNetworkTimesReachTheInstance(t *testing.T) {
	for _, c := range []struct{ words, scope string }{
		{` delay="2s" duration="6s"`, ` data-kitwork-scope="delay: 2000; duration: 6000"`},
		{` duration="6s" delay="2s"`, ` data-kitwork-scope="delay: 2000; duration: 6000"`},
		{` delay="800ms"`, ` data-kitwork-scope="delay: 800"`},
		{` duration="10s"`, ` data-kitwork-scope="duration: 10000"`},
		{` delay="0" duration="0"`, ` data-kitwork-scope="delay: 0; duration: 0"`},
		{` delay="0ms" duration="0s"`, ` data-kitwork-scope="delay: 0; duration: 0"`},
		{` delay="0.0"`, ` data-kitwork-scope="delay: 0"`},
		{` duration="0"`, ` data-kitwork-scope="duration: 0"`},
		{` delay=" 2s "`, ` data-kitwork-scope="delay: 2000"`},
		{` delay="1.5s"`, ` data-kitwork-scope="delay: 1500"`},
		{` delay="0.25s" duration="4.03s"`, ` data-kitwork-scope="delay: 250; duration: 4030"`},
		{` duration="2.01s"`, ` data-kitwork-scope="duration: 2010"`},
		{` delay="100ms"`, ` data-kitwork-scope="delay: 100"`},
		{` delay="10s" duration="60s"`, ` data-kitwork-scope="delay: 10000; duration: 60000"`},
		{` delay="10000ms" duration="60000ms"`, ` data-kitwork-scope="delay: 10000; duration: 60000"`},
		{` duration="2s"`, ` data-kitwork-scope="duration: 2000"`},
		{``, ``},
	} {
		source := `<content>` + networkWithNotice(` as="$network"`+c.words) + `</content>`
		host, html, warned := networkCompile(t, source)
		if want := `<div class="flex-column" data-kitwork-component="network: $network" role="status"` + c.scope + `>`; host != want {
			t.Errorf("%q:\n got  %s\n want %s", c.words, host, want)
		}
		if warned != "" {
			t.Errorf("%q says nothing, got:\n%s", c.words, warned)
		}
		if c.scope == "" {
			mustNotContain(t, html, `data-kitwork-scope`)
		}
	}

	// placed, the words go with the place's classes; not written, no scope either
	for words, want := range map[string]string{
		` place="bottom-start" delay="3s"`: `<div class="flex-column network network-bottom-start" data-kitwork-component="network" role="status" data-kitwork-scope="delay: 3000">`,
		` place="bottom-start"`:            `<div class="flex-column network network-bottom-start" data-kitwork-component="network" role="status">`,
	} {
		host, html, warned := networkCompile(t, `<content>`+networkWithNotice(words)+`</content>`)
		if host != want || warned != "" {
			t.Errorf("%q:\n got  %s (warnings %q)\n want %s", words, host, warned, want)
		}
		if !strings.Contains(words, "delay") {
			mustNotContain(t, html, `data-kitwork-scope`)
		}
	}

	// written empty: delay goes in, quietly; nothing written, nothing goes in
	for words, want := range map[string]string{
		` delay="2s"`: `<div class="flex-column show-false" data-kitwork-component="network" data-kitwork-event="offline: close()" data-kitwork-scope="delay: 2000">`,
		` delay="0"`:  `<div class="flex-column show-false" data-kitwork-component="network" data-kitwork-event="offline: close()" data-kitwork-scope="delay: 0">`,
		``:            `<div class="flex-column show-false" data-kitwork-component="network" data-kitwork-event="offline: close()">`,
	} {
		host, html, warned := networkCompile(t, `<content><box component="network"`+words+` offline="close()" /></content>`)
		if host != want || warned != "" {
			t.Errorf("empty%q:\n got  %s (warnings %q)\n want %s", words, host, warned, want)
		}
		if words == "" {
			mustNotContain(t, html, `data-kitwork-scope`)
		}
	}
}

// inputs are the times written, delay before duration, each its milliseconds as a number literal the
// kernel reads (the source the scope writes, the same text as the value): a zero written is written,
// since it is a choice (say at once, stay until closed); a time not written is not.
func TestNetworkInputsAreTheTimesWritten(t *testing.T) {
	zero, two, back := 0.0, 2000.0, 4030.0
	for _, c := range []struct {
		spec networkSpec
		want string
	}{
		{networkSpec{}, ""},
		{networkSpec{delay: &two}, "delay: 2000"},
		{networkSpec{duration: &zero}, "duration: 0"},
		{networkSpec{delay: &zero, duration: &two}, "delay: 0; duration: 2000"},
		{networkSpec{duration: &back, delay: &two, place: "top", status: true}, "delay: 2000; duration: 4030"},
	} {
		var pairs []string
		for _, in := range c.spec.inputs() {
			if len(in.in.parts) != 1 || in.in.parts[0].text != in.source {
				t.Errorf("%s: the value is not the text %q alone: %+v", in.name, in.source, in.in.parts)
			}
			pairs = append(pairs, in.name+": "+in.source)
		}
		if got := strings.Join(pairs, "; "); got != c.want {
			t.Errorf("inputs: got %q, want %q", got, c.want)
		}
	}
}

// What delay and duration do not take is an error at the word, saying what to write: bare or empty, a
// value from data, a prefix (they are decided once, when the page compiles), a negative, a number
// without its unit (a large one is milliseconds), what is no duration (a minus before a word is no
// negative), longer than the limit (delay 10s: a page that does not answer goes untold for too long;
// duration 60s, the toast's: stay until closed is 0; digits too many to hold included). The words other
// libraries give these two times are told which of the two to write, with or without a prefix. Errors
// come before the empty host's warning: an empty host is told them the same.
// CONTROL: each written right reaches the instance quietly (TestNetworkTimesReachTheInstance); each
// branch of networkTime, the limits and the strangers were disabled by hand and their rows went red.
func TestNetworkTimesWrittenWrongAreErrors(t *testing.T) {
	const delayEmpty = `delay cần một thời lượng: viết delay="1s" (0 là báo ngay)`
	const durationEmpty = `duration cần một thời lượng: viết duration="4s" (0 là ở lại tới khi đóng)`
	const delayLong = ` dài hơn 10 giây: mất mạng lâu vậy mà không báo thì người xem không biết vì sao trang không chạy; viết delay="10s" hay ngắn hơn`
	const durationLong = ` dài hơn 60 giây: khung ở lại tới khi đóng thì viết duration="0"`
	const toDelay = `chờ mạng mất bao lâu rồi mới báo là delay (delay="1s"); khung "đã có mạng lại" ở lại bao lâu là duration (duration="4s")`
	const toDuration = `khung "đã có mạng lại" ở lại bao lâu rồi tự đóng là duration (duration="4s", 0 là ở lại tới khi đóng); chờ mạng mất bao lâu rồi mới báo là delay (delay="1s")`
	nines := strings.Repeat("9", 400)
	for _, c := range []struct{ words, needle, says string }{
		{` delay`, `delay>`, delayEmpty},
		{` delay=""`, `delay=`, delayEmpty},
		{` delay="  "`, `delay=`, delayEmpty},
		{` duration`, `duration>`, durationEmpty},
		{` duration=""`, `duration=`, durationEmpty},

		{` delay="{{ wait }}"`, `delay=`, `delay của network là một thời lượng cố định (delay="1s"), không có {{ }}: nó được quyết một lần khi trang dựng`},
		{` duration="{{ seconds }}s"`, `duration=`, `duration của network là một thời lượng cố định (duration="4s"), không có {{ }}: nó được quyết một lần khi trang dựng`},

		{` medium:delay="1s"`, `medium:delay=`, `delay của network không nhận tiền tố: nó được quyết một lần khi trang dựng`},
		{` dark:duration="6s"`, `dark:duration=`, `duration của network không nhận tiền tố: nó được quyết một lần khi trang dựng`},

		{` delay="-1s"`, `delay=`, `delay không nhận thời lượng âm (-1s): viết một thời lượng như delay="1s"`},
		{` delay="-800ms"`, `delay=`, `delay không nhận thời lượng âm (-800ms): viết một thời lượng như delay="1s"`},
		{` duration="-3"`, `duration=`, `duration không nhận thời lượng âm (-3): viết một thời lượng như duration="4s"`},

		{` delay="3"`, `delay=`, `delay="3" thiếu đơn vị: viết delay="3s"`},
		{` delay="800"`, `delay=`, `delay="800" thiếu đơn vị: viết delay="800ms"`},
		{` duration="4.5"`, `duration=`, `duration="4.5" thiếu đơn vị: viết duration="4.5s"`},
		{` duration="2500"`, `duration=`, `duration="2500" thiếu đơn vị: viết duration="2500ms"`},

		{` delay="soon"`, `delay=`, `delay="soon" không phải một thời lượng: viết delay="1s" hay delay="800ms" (0 là báo ngay)`},
		{` delay="-fast"`, `delay=`, `delay="-fast" không phải một thời lượng: viết delay="1s" hay delay="800ms" (0 là báo ngay)`},
		{` delay="1m"`, `delay=`, `delay="1m" không phải một thời lượng: viết delay="1s" hay delay="800ms" (0 là báo ngay)`},
		{` duration="4 s"`, `duration=`, `duration="4 s" không phải một thời lượng: viết duration="4s" hay duration="800ms" (0 là ở lại tới khi đóng)`},
		{` duration="4S"`, `duration=`, `duration="4S" không phải một thời lượng: viết duration="4s" hay duration="800ms" (0 là ở lại tới khi đóng)`},
		{` duration=".5s"`, `duration=`, `duration=".5s" không phải một thời lượng: viết duration="4s" hay duration="800ms" (0 là ở lại tới khi đóng)`},

		{` delay="10.5s"`, `delay=`, `delay="10.5s"` + delayLong},
		{` delay="10001ms"`, `delay=`, `delay="10001ms"` + delayLong},
		{` delay="` + nines + `s"`, `delay=`, `delay="` + nines + `s"` + delayLong},
		{` duration="61s"`, `duration=`, `duration="61s"` + durationLong},
		{` duration="60001ms"`, `duration=`, `duration="60001ms"` + durationLong},
		{` duration="` + nines + `ms"`, `duration=`, `duration="` + nines + `ms"` + durationLong},

		{` timeout="3s"`, `timeout=`, `network không có timeout: ` + toDuration},
		{` time="3s"`, `time=`, `network không có time: ` + toDuration},
		{` autoclose="3s"`, `autoclose=`, `network không có autoclose: ` + toDuration},
		{` autoClose="3s"`, `autoClose=`, `network không có autoClose: ` + toDuration},
		{` wait="2s"`, `wait=`, `network không có wait: ` + toDelay},
		{` debounce="2s"`, `debounce=`, `network không có debounce: ` + toDelay},
		{` grace="2s"`, `grace=`, `network không có grace: ` + toDelay},
		{` medium:timeout="3s"`, `medium:timeout=`, `network không có timeout: ` + toDuration},
	} {
		source := `<content>` + networkWithNotice(c.words) + `</content>`
		if got, want := compileError(t, content(source)), where("content.ui", source, c.needle)+c.says; got != want {
			t.Errorf("%.60q:\n got  %.400s\n want %.400s", c.words, got, want)
		}
	}

	// the empty host is told the same, before any warning
	for _, c := range []struct{ words, needle, says string }{
		{` duration="90s"`, `duration=`, `duration="90s"` + durationLong},
		{` duration="4"`, `duration=`, `duration="4" thiếu đơn vị: viết duration="4s"`},
		{` delay="11s"`, `delay=`, `delay="11s"` + delayLong},
		{` timeout="3s"`, `timeout=`, `network không có timeout: ` + toDuration},
		{` grace="2s"`, `grace=`, `network không có grace: ` + toDelay},
	} {
		source := `<content><box component="network"` + c.words + ` offline="close()" /></content>`
		if got, want := compileError(t, content(source)), where("content.ui", source, c.needle)+c.says; got != want {
			t.Errorf("empty%q:\n got  %s\n want %s", c.words, got, want)
		}
	}
}

// What works and is probably not meant is a warning at the word, said once, and the page renders as
// written: a duration under 2s on a notice (the toast's least: read too fast); a duration on an empty
// host, which shows nothing for it to close (told once: how fast it would read is beside the point
// there, and 2s would still be told), and goes in no scope, as place does not. A place and a duration on
// one empty host are each told at their own word.
// CONTROL: 2s, 0 and a short delay say nothing (TestNetworkTimesReachTheInstance); the warnings, their
// limit and the empty-host check were each disabled by hand and their rows went red.
func TestNetworkTimesWarnings(t *testing.T) {
	const fast = `dưới 2 giây: người xem đọc không kịp; viết duration từ "2s", hay bỏ duration (4 giây)`
	const empty = `trên network rỗng: thẻ chủ rỗng không hiện gì, duration không đóng gì; vẽ thông báo bên trong (<card show="open">…</card>) hay bỏ duration`
	emptyHost := `<div class="flex-column show-false" data-kitwork-component="network" data-kitwork-event="offline: close()">`
	for _, c := range []struct {
		name, host string
		said       []string // needle, message: one warning each, in this order
		want       string   // the host as rendered
	}{
		{"a second", networkWithNotice(` duration="1s"`), []string{`duration=`, `duration="1s" ` + fast},
			`<div class="flex-column" data-kitwork-component="network" role="status" data-kitwork-scope="duration: 1000">`},
		{"just under two seconds", networkWithNotice(` delay="0" duration="1999ms"`), []string{`duration=`, `duration="1999ms" ` + fast},
			`<div class="flex-column" data-kitwork-component="network" role="status" data-kitwork-scope="delay: 0; duration: 1999">`},
		{"half a second", networkWithNotice(` duration="0.5s"`), []string{`duration=`, `duration="0.5s" ` + fast},
			`<div class="flex-column" data-kitwork-component="network" role="status" data-kitwork-scope="duration: 500">`},
		{"a duration on an empty host", `<box component="network" duration="6s" offline="close()" />`, []string{`duration=`, `duration="6s" ` + empty}, emptyHost},
		{"a fast duration on an empty host is told once", `<box component="network" duration="1s" offline="close()" />`, []string{`duration=`, `duration="1s" ` + empty}, emptyHost},
		{"a zero duration on an empty host", `<box component="network" duration="0" offline="close()" />`, []string{`duration=`, `duration="0" ` + empty}, emptyHost},
		{"an empty host keeps its delay", `<box component="network" delay="2s" duration=" 6s " offline="close()" />`, []string{`duration=`, `duration="6s" ` + empty},
			`<div class="flex-column show-false" data-kitwork-component="network" data-kitwork-event="offline: close()" data-kitwork-scope="delay: 2000">`},
		{"a place and a duration on an empty host", `<box component="network" place="top" duration="6s" offline="close()" />`, []string{
			`place=`, `place="top" trên network rỗng: thẻ chủ rỗng chỉ nghe sự kiện và không hiện, place không đặt gì; vẽ thông báo bên trong (<card show="open">…</card>) hay bỏ place`,
			`duration=`, `duration="6s" ` + empty}, emptyHost},
	} {
		t.Run(c.name, func(t *testing.T) {
			source := `<content>` + c.host + `</content>`
			host, _, warned := networkCompile(t, source)
			var want []string
			for i := 0; i < len(c.said); i += 2 {
				want = append(want, where("content.ui", source, c.said[i])+c.said[i+1])
			}
			if warned != strings.Join(want, "\n") {
				t.Errorf("warnings:\n got  %s\n want %s", warned, strings.Join(want, "\n"))
			}
			if host != c.want {
				t.Errorf("host:\n got  %s\n want %s", host, c.want)
			}
		})
	}
}

// An empty host with an alias may be drawn elsewhere: a card of the page shows while $network.open, so the
// host's duration is how long "back online" stays there, and it reaches the instance with no warning.
// CONTROL: the same empty host without an alias is told its duration closes nothing, and drops it.
func TestNetworkEmptyHostWithAnAliasKeepsItsDuration(t *testing.T) {
	content := `<content><card show="$network.open"><text>{{ $network.connected ? 'Back online' : 'Offline' }}</text></card></content>`
	html := compileQuiet(t, toastSite(networkToast+`<box component="network" as="$network" duration="6s" />`, content))
	mustContain(t, html, `data-kitwork-component="network: $network" data-kitwork-scope="duration: 6000"`)

	s := memorySite("", toastSite(networkToast+`<box component="network" duration="6s" />`, `<content><text>x</text></content>`))
	page, err := s.Compile("/site")
	if err != nil {
		t.Fatal(err)
	}
	if warned := terminalWarnings(page); !strings.Contains(warned, `duration="6s" trên network rỗng`) {
		t.Errorf("an empty host without an alias is not told its duration closes nothing:\n%s", warned)
	}
	if strings.Contains(page.Render(Request{Path: "/"}), "duration: 6000") {
		t.Error("an empty host without an alias keeps the duration it was told closes nothing")
	}
}
