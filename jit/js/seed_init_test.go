package js

import "testing"

// data-kit-seed lands BEFORE init (02/10): a component reading its seeded state in init sees the
// markup's values — a plain key and a list[] alike — not its own defaults. The second host has no
// seed, so its init sees the default: the control that init still runs, and runs once.
func TestBrowserSeedLandsBeforeInit(t *testing.T) {
	runBrowserProof(t, "seed before init", nil, `
<div id="seeded" data-kit-component="probe">
  <span data-kit-seed:data-name="name" data-name="Ada"></span>
  <i data-kit-seed:data-tag="tags[]" data-tag="one"></i><i data-kit-seed:data-tag="tags[]" data-tag="two"></i>
</div>
<div id="plain" data-kit-component="probe"></div>
<script>
kit.component("probe", {
  name: "default",
  tags: [],
  sawName: "",
  sawTags: "",
  inits: 0,
  init: function () { this.sawName = this.name; this.sawTags = this.tags.join(","); this.inits++; }
});
(async function () {
  await wait(200);
  var seeded = window.kit.scopeFor(document.getElementById("seeded"));
  var plain = window.kit.scopeFor(document.getElementById("plain"));
  assert(seeded.sawName === "Ada", "init must see the seeded name, saw " + JSON.stringify(seeded.sawName));
  assert(seeded.sawTags === "one,two", "init must see the seeded list, saw " + JSON.stringify(seeded.sawTags));
  assert(plain.sawName === "default" && plain.inits === 1 && seeded.inits === 1, "each init runs once, the unseeded one on its defaults: " + plain.sawName + " " + plain.inits + " " + seeded.inits);
  window.kit.render(); await wait(50);
  assert(seeded.inits === 1, "a later paint does not run init again");
  root.setAttribute("data-kit-test", "passed");
})();
</script>`)
}
