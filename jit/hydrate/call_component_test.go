package hydrate

import (
	"strings"
	"testing"
)

// call.js for a component of the site whose jobs go through the bridge (28/10/2026, ui/component_live.go): the
// page holds two carts, as the server draws them (each host naming the component and its place), and the
// components it holds at the end of its content. Each job is called on the instance it runs in: the call names
// the place and carries that instance's vars; the answer's patch lands in that instance, and .pending and
// .error are that instance's.
const componentPage = `
var kit = window.kit;
function cart(at) {
  var host = el("div", { "data-kitwork-component": "shop-cart", "data-kitwork-at": at });
  var count = el("p", { "data-kitwork-bind": "text: items.length" });
  var done = el("p", { "data-kitwork-bind": "show: done", "hidden": "" });
  var button = el("button", { "data-kitwork-event": "click: checkout()", "data-kitwork-bind": "disabled: checkout.pending" });
  var notice = el("p", { "data-kitwork-bind": "text: checkout.error ? checkout.error.message : ''" });
  host.appendChild(count); host.appendChild(done); host.appendChild(button); host.appendChild(notice);
  document.body.appendChild(host);
  return { host: host, count: count, done: done, button: button, notice: notice };
}
var one = cart("c1"), two = cart("c2");
var block = el("script", { "type": "application/json", "data-kitwork-components": "" });
block.textContent = JSON.stringify({ "shop-cart": { state: { items: [], done: false }, jobs: ["add", "checkout"], vars: ["done", "items"] } });
document.body.appendChild(block);

var out = [], sent = [], refuse = false;
global.fetch = function (url, options) {
  var body = JSON.parse(options.body);
  sent.push({ job: new URL(url, "http://x").searchParams.get("kitwork-call"), body: body });
  var answer = refuse ? { seq: body.seq, error: { code: 500, message: "hết hàng" } } : { seq: body.seq, components: {} };
  if (!refuse) answer.components[body.at] = { patch: { items: [], done: true } };
  return new Promise(function (resolve) {
    out.push(function () {
      resolve({ redirected: false, ok: !refuse, status: refuse ? 500 : 200, headers: { get: function () { return "application/json"; } }, json: function () { return Promise.resolve(answer); } });
    });
  });
};
function tick() { return new Promise(function (r) { setTimeout(r, 0); }); }
async function deliver() {
  for (var round = 0; round < 10 && out.length; round++) {
    var now = out.splice(0, out.length);
    for (var i = 0; i < now.length; i++) { now[i](); await tick(); }
    await tick();
  }
}
function click(button) {
  document.dispatchEvent({ type: "click", target: button, defaultPrevented: false, preventDefault: function () {}, stopPropagation: function () {} });
}
function instance(c) { return kit.state(c.host).scope; }
` + "\n__CALL_JS__\n"

const componentAssertions = `
(async function () {
  kit.render();
  if (typeof instance(one).checkout !== "function" || instance(one).done !== false) throw new Error("each cart should start from what the page wrote, its jobs functions");
  instance(one).items = [{ id: 1, name: "cà phê" }];
  kit.render();
  click(one.button);
  kit.render();
  if (sent.length !== 1 || sent[0].job !== "shop-cart.checkout") throw new Error("the call should name the component and the job: " + JSON.stringify(sent));
  var body = sent[0].body;
  if (body.at !== "c1") throw new Error("the call should name the place of the instance clicked, got " + body.at);
  if (Object.keys(body.scope).sort().join(",") !== "done,items" || body.scope.items.length !== 1) throw new Error("the call should carry the instance's vars, nothing else: " + JSON.stringify(body.scope));
  if (one.button.disabled !== true || two.button.disabled === true) throw new Error("the cart whose call is out is pending, the other is not");
  await deliver();
  kit.render();
  if (instance(one).done !== true || instance(one).items.length !== 0) throw new Error("the instance should show the answer: " + JSON.stringify(instance(one)));
  if (instance(two).done !== false) throw new Error("the other cart should not change");
  if ("done" in kit.scope.$) throw new Error("the instance's data is not the page's");
  if (one.done.hidden !== false || two.done.hidden !== true) throw new Error("the instance's look should follow its data");
  if (one.button.disabled) throw new Error("after the answer the cart is not pending");

  refuse = true;
  click(two.button);
  await deliver();
  kit.render();
  if (sent[1].body.at !== "c2") throw new Error("the second call should name c2, got " + sent[1].body.at);
  if (two.notice.textContent !== "hết hàng" || one.notice.textContent !== "") throw new Error("the error belongs to the cart that called: " + JSON.stringify([one.notice.textContent, two.notice.textContent]));
  console.log("a component's job is called on its instance and answers into it");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func componentScript(callJS string) string {
	return strings.Replace(componentPage, "__CALL_JS__", callJS, 1) + componentAssertions
}

func TestCallOfAComponentJobLandsInItsInstance(t *testing.T) {
	runNodeDOMScript(t, "call_component.test.js", componentScript(callJS))
}

// The control: with the instance's patch not written, the cart that called keeps what it had and the test goes
// red at the instance (the answer reached call.js, and nothing put it where it belongs).
func TestCallOfAComponentJobWithoutItsPatchKeepsTheOldState(t *testing.T) {
	write := "if (instance) Object.keys(mine.patch || {}).forEach(function (k) { instance[k] = mine.patch[k]; });"
	if !strings.Contains(callJS, write) {
		t.Fatal("the patch of an instance in call.js moved: update this control")
	}
	out, err := runNodeDOMWith(t, composedRuntime(), componentScript(strings.Replace(callJS, write, "", 1)))
	if err == nil || !strings.Contains(out, "the instance should show the answer") {
		t.Fatalf("without the instance's patch the cart should keep its old state; got err=%v\n%s", err, out)
	}
}
