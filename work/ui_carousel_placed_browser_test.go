package work

import (
	"net/http"
	"testing"
)

// carouselPlacedFiles is a carousel whose arrows the author places over the slides and writes BEFORE the
// <list>, and whose slides each hold a caption placed over their picture (which makes a slide a
// positioned box, painted after what comes before it).
var carouselPlacedFiles = map[string]string{
	"localhost/router.work": `import * as router from "kitwork/router";

router.language("vi");
`,
	"localhost/view.ui": `<view><content /></view>`,
	"localhost/content.ui": `<content padding="4">
  <carousel as="$hero" alt="Ảnh" max-width="large">
    <box place="left" padding="3"><button id="before" click="previous()" background="white/85" color="black"><icon value="chevron-left" alt="Trước" /></button></box>
    <list>
      <item><box height="16rem" background="panel"><text>Một</text></box><box place="bottom-left" padding="3" background="black/60" color="white"><text>Chữ một</text></box></item>
      <item><box height="16rem" background="panel"><text>Hai</text></box><box place="bottom-left" padding="3" background="black/60" color="white"><text>Chữ hai</text></box></item>
    </list>
    <box place="right" padding="3"><button id="after" click="next()" background="white/85" color="black"><icon value="chevron-right" alt="Sau" /></button></box>
  </carousel>
</content>`,
}

// What the author places over a carousel's slides stands above them wherever it is written: an arrow
// written before the <list> is under the pointer at its own middle, not the slide that holds a placed
// caption (a positioned box painted after it), and a click there moves the carousel. The arrow after the
// <list> was always above. CONTROL: without the reboot's rule for the carousel's other children
// (z-index), the slide is what the pointer finds at the first arrow, and its click never comes.
func TestBrowserCarouselPlacedBeforeTheListStandsAbove(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the carousel browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, carouselPlacedFiles)
	if page := serveUI(t, tenant, "http://localhost/"); page.Code != http.StatusOK || warningsScriptRe.MatchString(page.Body.String()) {
		t.Fatalf("/ = %d, or it links warnings:\n%.2000s", page.Code, page.Body.String())
	}
	server := carouselServer(tenant)
	defer server.Close()
	d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
	d.loaded(exampleSettle)
	got := d.evaluate(`(async function () {
  var found = function (id) {
    var button = document.getElementById(id), box = button.getBoundingClientRect();
    var at = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
    return id + " " + (at && button.contains(at) ? "on top" : "under " + (at ? at.tagName.toLowerCase() : "nothing"));
  };
  var before = found("before"), after = found("after");
  var button = document.getElementById("before"), box = button.getBoundingClientRect();
  document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2).click();
  await new Promise(function (done) { setTimeout(done, 900); });
  return [before, after, "current " + kit.scope.$hero.current].join(" | ");
})()`)
	// previous() from the first comes round to the last: current 1
	if want := "before on top | after on top | current 1"; got != want {
		t.Errorf("the arrows placed over the slides:\n got  %s\n want %s", got, want)
	}
}
