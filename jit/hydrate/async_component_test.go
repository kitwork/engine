package hydrate

import "testing"

// Async belongs to trusted component JavaScript, not the closed expression grammar. The click
// effect observes a component method's Promise and repaints on both resolve and reject.
func TestAsyncComponentMethodRendersWhenPromiseSettles(t *testing.T) {
	const assertions = `
var kit = window.kit;

kit.component("native-demo", {
  photo: "",
  error: "",
  capture: function () {
    var self = this;
    return new Promise(function (resolve) {
      setTimeout(function () {
        self.photo = "kitwork-cache://avatar.jpg";
        resolve(self.photo);
      }, 5);
    });
  },
  fail: function () {
    return Promise.reject(new Error("camera denied"));
  }
});

var app = el("section", { "data-kit-component": "native-demo" });
var capture = el("button", { "data-kit-click": "capture()" });
var fail = el("button", { "data-kit-click": "fail()" });
var photo = el("b", { "data-kit-text": "photo" });
var error = el("i", { "data-kit-text": "error" });
app.appendChild(capture);
app.appendChild(fail);
app.appendChild(photo);
app.appendChild(error);
document.body.appendChild(app);
kit.render();

document.dispatchEvent({ type: "click", target: capture });
setTimeout(function () {
  if (photo.textContent !== "kitwork-cache://avatar.jpg") {
    throw new Error("resolved async component method did not repaint: " + photo.textContent);
  }

  document.dispatchEvent({ type: "click", target: fail });
  setTimeout(function () {
    if (error.textContent !== "camera denied") {
      throw new Error("rejected async component method did not expose/repaint error: " + error.textContent);
    }
    console.log("async component effects: resolve + reject repaint");
  }, 10);
}, 20);
`
	runNodeDOMScript(t, "async_component.test.js", assertions)
}

func TestAsyncComponentInitRendersWhenPromiseSettles(t *testing.T) {
	const assertions = `
var kit = window.kit;

kit.component("async-init-ok", {
  status: "starting",
  init: function () {
    var self = this;
    return Promise.resolve().then(function () {
      self.status = "ready";
    });
  }
});
kit.component("async-init-fail", {
  error: "",
  init: function () {
    return Promise.reject(new Error("permission denied"));
  }
});

var ok = el("section", { "data-kit-component": "async-init-ok" });
var status = el("b", { "data-kit-text": "status" });
ok.appendChild(status);
var failed = el("section", { "data-kit-component": "async-init-fail" });
var error = el("i", { "data-kit-text": "error" });
failed.appendChild(error);
document.body.appendChild(ok);
document.body.appendChild(failed);
kit.render();

setTimeout(function () {
  if (status.textContent !== "ready") {
    throw new Error("resolved async init did not repaint: " + status.textContent);
  }
  if (error.textContent !== "permission denied") {
    throw new Error("rejected async init did not expose/repaint error: " + error.textContent);
  }
  console.log("async component init: resolve + reject repaint");
}, 20);
`
	runNodeDOMScript(t, "async_component_init.test.js", assertions)
}
