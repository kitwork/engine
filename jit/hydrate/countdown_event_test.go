package hydrate

import (
	"strings"
	"testing"
)

// finish is the event Kitwork's countdown sends (ui/countdown.go, 19/10/2026): on the wire it is a
// custom event of the component, known here as network's offline and online are, so the countdown the
// engine writes (its host and its time left, as ui compiles the decision's example) is no note on the
// page. A name that is no event the kernel knows is still told.
// CONTROL: "finished" is told; with finish taken out of ComponentEvents this test failed, the
// countdown's host told "finish" is not an event the kernel knows (run 19/10/2026 on an overlaid copy
// of grouped_check.go).
func TestCountdownFinishIsAKnownEvent(t *testing.T) {
	const countdown = `<div class="flex-row gap-3" data-kitwork-component="countdown: $tea" ` +
		`data-kitwork-event="ready: play(); finish: $toast.success(&#39;Trà đã sẵn sàng&#39;)" data-kitwork-scope="duration: 300000">` +
		`<p>Còn <time class="countdown-time" role="timer" data-kitwork-bind="datetime: &#39;PT&#39; + hours + &#39;H&#39; + minutes + &#39;M&#39; + seconds + &#39;S&#39;; ` +
		`text: [(minutes), &#39;:&#39;, (String(seconds).padStart(2, &#39;0&#39;))].join(&#39;&#39;)" datetime="PT0H5M0S">5:00</time></p>` +
		`<button type="button" data-kitwork-event="click: toggle()">Chạy</button></div>`
	if report := verifyOf(t, countdown); report != "" {
		t.Errorf("the countdown the engine writes is told:\n%s", report)
	}
	if report := verifyOf(t, `<b data-kitwork-event="finish.once: play()"></b>`); report != "" {
		t.Errorf("finish with a modifier is told:\n%s", report)
	}
	// CONTROL
	if report := verifyOf(t, `<b data-kitwork-event="finished: play()"></b>`); !strings.Contains(report, `"finished" is not an event the kernel knows`) {
		t.Errorf("a name that is no event is not told, got:\n%s", report)
	}
}
