package js

import (
	"strings"
	"testing"
)

func TestCarouselIsShippedWhenUsed(t *testing.T) {
	out := deliver(`<div data-kit-component="carousel"><div data-kit-element="track"></div></div>`)
	if !strings.Contains(out, `/_kitwork/components/carousel.`) {
		t.Fatalf("carousel was not injected for markup that uses it:\n%s", out)
	}
	if src := ModulesJS([]string{"carousel"}); !strings.Contains(src, `component("carousel", `) || strings.Contains(src, `component("carousel@`) {
		t.Error("the file must register the bare name, and only it")
	}
}

// The browser moves the track; the component answers which slide is showing and scrolls to one on
// request. The proof moves the track BY HAND (as a swipe would) and checks the dots follow, then
// drives the buttons and checks the track follows.
func TestBrowserCarouselAnswersWhichSlideIsShowing(t *testing.T) {
	slide := func(n, label string) string {
		return `<div data-kit-element="slide" role="group" aria-roledescription="slide" aria-label="` + label +
			`" style="flex:0 0 100%;height:120px;scroll-snap-align:start">slide ` + n + `</div>`
	}
	dot := func(n string) string {
		return `<button id="dot` + n + `" role="tab" data-kit-click="go(` + n + `)" data-kit-bind:aria-selected="active === ` + n + ` ? 'true' : 'false'">` + n + `</button>`
	}
	runBrowserProof(t, "carousel", []string{"carousel"}, `
<div data-kit-component="carousel" role="group" aria-roledescription="carousel" aria-label="Highlights" style="width:400px">
  <div id="track" data-kit-element="track" tabindex="0" style="display:flex;overflow-x:auto;scroll-snap-type:x mandatory;width:400px">
    `+slide("0", "1 of 3")+slide("1", "2 of 3")+slide("2", "3 of 3")+`
  </div>
  <button id="prev" data-kit-click="previous()">prev</button>
  `+dot("0")+dot("1")+dot("2")+`
  <button id="next" data-kit-click="next()">next</button>
  <output id="label" data-kit-text="(active + 1) + ' of ' + count"></output>
</div>
<script>
(async function () {
  await wait(80);
  var track = document.getElementById("track"), label = document.getElementById("label");
  var selected = function () { return [0, 1, 2].filter(function (n) { return document.getElementById("dot" + n).getAttribute("aria-selected") === "true"; }).join(","); };

  assert(label.textContent === "1 of 3" && selected() === "0", "starts on the first slide, got " + label.textContent + " / dot " + selected());

  // A swipe is the browser's: move the track by hand and the answer follows.
  track.scrollLeft = 800; track.dispatchEvent(new Event("scroll")); await wait(60);
  assert(label.textContent === "3 of 3" && selected() === "2", "scrolled to the third slide by hand, the component should say so, got " + label.textContent);

  // next() from the last slide comes back to the first — the track follows the button.
  document.getElementById("next").click(); await wait(80);
  assert(track.scrollLeft === 0 && selected() === "0", "next from the last returns to the first, scrollLeft=" + track.scrollLeft + " dot " + selected());

  // previous() from the first goes to the last.
  document.getElementById("prev").click(); await wait(80);
  assert(track.scrollLeft === 800 && label.textContent === "3 of 3", "previous from the first goes to the last, scrollLeft=" + track.scrollLeft);

  // A dot goes straight to its slide.
  document.getElementById("dot1").click(); await wait(80);
  assert(track.scrollLeft === 400 && selected() === "1", "a dot scrolls to its slide, scrollLeft=" + track.scrollLeft);

  root.setAttribute("data-kit-test", "passed");
})().catch(function (error) { var root = document.documentElement; if (!root.hasAttribute("data-kit-test")) { root.setAttribute("data-kit-test", "failed"); root.setAttribute("data-kit-test-error", String(error && error.message || error)); } });
</script>`)
}
