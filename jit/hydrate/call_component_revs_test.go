package hydrate

import (
	"strings"
	"testing"
)

// The revisions of an instance's data (29/10/2026, work/component_call.go): the block brings those of each
// instance by place; a call of a job of one sends that instance's, never the other's; an answer brings new ones,
// which the next call sends. The two carts of componentPage, each with a total the server computes.
const componentRevsAssertions = `
(async function () {
  global.fetch = function (url, options) {
    var body = JSON.parse(options.body);
    sent.push({ job: new URL(url, "http://x").searchParams.get("kitwork-call"), body: body });
    var answer = { seq: body.seq, bridge: { revs: {} } };
    answer[body.at] = { items: [], done: true };
    answer.bridge.revs[body.at] = { total: body.at === "c1" ? "t1" : "u1" };
    return Promise.resolve({ redirected: false, ok: true, status: 200, headers: { get: function () { return "application/json"; } }, json: function () { return Promise.resolve(answer); } });
  };
  kitwork.render();
  click(one.button);
  await tick(); await tick(); await tick();
  if (JSON.stringify(sent[0].body.bridge.revs.c1) !== JSON.stringify({ total: "t0" })) throw new Error("a call should send the revisions of its instance's data the block brought, sent " + JSON.stringify(sent[0].body));
  click(two.button);
  await tick(); await tick(); await tick();
  if (JSON.stringify(sent[1].body.bridge.revs.c2) !== JSON.stringify({ total: "u0" })) throw new Error("a call of the other cart should send that cart's revisions, sent " + JSON.stringify(sent[1].body));
  click(one.button);
  await tick(); await tick(); await tick();
  if (JSON.stringify(sent[2].body.bridge.revs.c1) !== JSON.stringify({ total: "t1" })) throw new Error("the next call should send the revisions the answer brought, sent " + JSON.stringify(sent[2].body));
  console.log("each instance sends the revisions it holds, and keeps those an answer brings");
})().catch(function (e) { console.error(e && e.stack || e); process.exit(1); });
`

func componentRevsScript(callJS string) string {
	page := strings.Replace(componentPage, `bridge: { defaults: { "shop-cart": { items: [], done: false } } } });`, `bridge: { defaults: { "shop-cart": { items: [], done: false } }, revs: { c1: { total: "t0" }, c2: { total: "u0" } } } });`, 1)
	return strings.Replace(page, "__CALL_JS__", callJS, 1) + componentRevsAssertions
}

func TestAnInstanceSendsTheRevisionsItHolds(t *testing.T) {
	if !strings.Contains(componentRevsScript(callJS), `revs: { c1:`) {
		t.Fatal("the components block of the test page moved: update this test")
	}
	runNodeDOMScript(t, "call_component_revs.test.js", componentRevsScript(callJS))
}

// The controls: a call that sends no revisions, and one that keeps not what the answer brought, are caught.
func TestAnInstanceThatSendsWrongRevisionsIsCaught(t *testing.T) {
	cases := []struct{ name, from, to, want string }{
		{"none sent", "      if (scopeRevs[call.at]) held[call.at] = scopeRevs[call.at];\n", "", "the block brought"},
		{"the answer's not kept", "        if (all[call.at][k]) kept[k] = all[call.at][k];", "        if (false) kept[k] = all[call.at][k];", "the answer brought"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(callJS, c.from) {
				t.Fatal("call.js moved: update this control")
			}
			out, err := runNodeDOMWith(t, composedRuntime(), componentRevsScript(strings.Replace(callJS, c.from, c.to, 1)))
			if err == nil || !strings.Contains(out, c.want) {
				t.Fatalf("%s should be caught; got err=%v\n%s", c.name, err, out)
			}
		})
	}
}
