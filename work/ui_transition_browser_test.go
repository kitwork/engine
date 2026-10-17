package work

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// transitionSite is a site whose toast and placed network come and go by one transition (written on
// both hosts), each at the place given, with two networks in the flow of its page under them: one
// written transition="fade", one written without it (none, the default there). Each network counts the
// network gone at once (delay="0"); each notice of a network has an id, its close button too.
func transitionSite(transition, toastPlace, networkPlace string) map[string]string {
	notice := func(id, words string) string {
		return `    <card id="` + id + `" show="open" flex="row" gap="3" padding="3" shadow="large">
      <text>` + words + `</text>
      <button id="close-` + id + `" click="close()"><icon value="x" alt="Đóng" /></button>
    </card>
`
	}
	return map[string]string{
		"localhost/router.work": `import { router } from "kitwork";

router.language("vi");
`,
		"localhost/page.ui": `<page><content /><overlay /></page>`,
		"localhost/overlay.ui": `<overlay>
  <box component="toast" as="$toast" place="` + toastPlace + `" transition="` + transition + `" gap="2">
    <card for="notice of notices; key: notice.id" flex="row" gap="3" padding="3" shadow="large">
      <text>{{ notice.message }}</text>
      <button click="dismiss(notice.id)"><icon value="x" alt="Đóng thông báo" /></button>
    </card>
  </box>
  <box component="network" as="$network" place="` + networkPlace + `" transition="` + transition + `" delay="0">
` + notice("placed", "Bạn đang ngoại tuyến") + `  </box>
</overlay>`,
		"localhost/content.ui": `<content padding="4" gap="2">
  <text>Trang</text>
  <box component="network" as="$faded" transition="fade" delay="0">
` + notice("faded", "Ngoại tuyến, hiện dần") + `  </box>
  <box component="network" as="$plain" delay="0">
` + notice("plain", "Ngoại tuyến, hiện ngay") + `  </box>
</content>`,
	}
}

// transitionNotices are the notices the proof watches: the toast's (the one row its blueprint draws),
// the placed network's, and the two in the flow.
var transitionNotices = []string{"toast", "placed", "faded", "plain"}

// transitionProbe samples the notices on their way in and out, installed once a page has loaded.
// hosts() is the classes of the four hosts that say where a notice sits and how it moves (toast-*,
// network-*, transition-*; "-" for none). watch(names, act, least) runs act, then reads each notice
// named in every frame (requestAnimationFrame) from the next one: the time since act, whether the
// notice is in the page, its computed display, opacity and translate (y in px), and every transition the
// browser runs on it then (document.getAnimations(): its property, from and to as its keyframes say, its
// duration). It reads for at least least milliseconds, and on while a transition of a notice runs, until
// three frames have passed without one (two seconds at most).
const transitionProbe = `(function () {
  if (window.__motion) return "ready";
  var toast = document.querySelector('[data-kitwork-component^="toast"]');
  var finds = {
    toast: function () { return toast.querySelector(":scope > [data-kit-key]"); },
    placed: function () { return document.getElementById("placed"); },
    faded: function () { return document.getElementById("faded"); },
    plain: function () { return document.getElementById("plain"); }
  };
  var y = function (translate) {
    if (!translate || translate === "none") return 0;
    var parts = translate.split(" ");
    return parts.length > 1 ? parseFloat(parts[1]) : 0;
  };
  var running = function (el) {
    return document.getAnimations().filter(function (a) { return a.effect && a.effect.target === el && a.playState === "running"; });
  };
  var read = function (el, t, seen) {
    if (!el || !el.isConnected) return { t: t, gone: true };
    var style = getComputedStyle(el);
    running(el).forEach(function (a) {
      var frames = a.effect.getKeyframes(), name = a.transitionProperty || "animation " + a.animationName;
      var from = frames.length ? frames[0][name] : "?", to = frames.length ? frames[frames.length - 1][name] : "?";
      seen[name + " " + from + "→" + to + " " + a.effect.getTiming().duration + "ms"] = true;
    });
    return { t: t, display: style.display, opacity: parseFloat(style.opacity), y: y(style.translate) };
  };
  window.__motion = {
    hosts: function () {
      var classes = function (host) {
        var own = Array.prototype.filter.call(host.classList, function (c) { return /^(toast|network|transition)/.test(c); });
        return own.join(" ") || "-";
      };
      return [toast, finds.placed().parentElement, finds.faded().parentElement, finds.plain().parentElement].map(classes).join(" ; ");
    },
    watch: function (names, act, least) {
      return new Promise(function (done) {
        var samples = {}, seen = {};
        names.forEach(function (name) { samples[name] = []; seen[name] = {}; });
        var started = performance.now(), quiet = 0;
        act();
        (function frame() {
          requestAnimationFrame(function () {
            var t = performance.now() - started, moving = false;
            names.forEach(function (name) {
              var el = finds[name]();
              samples[name].push(read(el, t, seen[name]));
              if (el && running(el).length) moving = true;
            });
            quiet = moving ? 0 : quiet + 1;
            if ((t < least || quiet < 3) && t < 2000) { frame(); return; }
            var out = {};
            names.forEach(function (name) { out[name] = { samples: samples[name], transitions: Object.keys(seen[name]).sort() }; });
            done(JSON.stringify(out));
          });
        })();
      });
    }
  };
  return "installed";
})()`

// transitionSample is one read of a notice, in one frame.
type transitionSample struct {
	T       float64 `json:"t"`
	Gone    bool    `json:"gone"`
	Display string  `json:"display"`
	Opacity float64 `json:"opacity"`
	Y       float64 `json:"y"`
}

// transitionWatched is what watch read of one notice: its samples, and the transitions the browser ran
// on it.
type transitionWatched struct {
	Samples     []transitionSample `json:"samples"`
	Transitions []string           `json:"transitions"`
}

// transitionWatch runs act (a script) in the page and returns what watch read of the notices named.
func transitionWatch(t *testing.T, d *devtools, act string, names ...string) map[string]transitionWatched {
	t.Helper()
	d.evaluate(transitionProbe)
	quoted, _ := json.Marshal(names)
	raw := d.evaluate(fmt.Sprintf(`window.__motion.watch(%s, function () { %s }, 300)`, quoted, act))
	var out map[string]transitionWatched
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return out
}

// transitionTrace writes the samples of a notice on one line, for a failure to show: the time, then
// gone, or its display, opacity and translate y.
func transitionTrace(w transitionWatched) string {
	var line []string
	for _, s := range w.Samples {
		if s.Gone {
			line = append(line, fmt.Sprintf("%.0f gone", s.T))
			continue
		}
		line = append(line, fmt.Sprintf("%.0f %s %.2f %+.1f", s.T, s.Display, s.Opacity, s.Y))
	}
	return strings.Join(line, " | ")
}

// transitionList is the transitions the browser ran on a notice, in one line.
func transitionList(w transitionWatched) string {
	if len(w.Transitions) == 0 {
		return "no transition"
	}
	return strings.Join(w.Transitions, ", ")
}

// transitionAround says a time the samples read as the time the transition takes (ms), with the
// tolerance of a browser's frames: from a frame before it to a quarter second late, "~<ms>ms"; else the
// time read.
func transitionAround(t, ms float64) string {
	if t >= ms-25 && t <= ms+250 {
		return fmt.Sprintf("~%gms", ms)
	}
	return fmt.Sprintf("after %.0fms", t)
}

// transitionAtOnce is how soon after the first read a change counts as at once: by the frame after it
// (a transition the reboot's reduced motion makes 0.01ms long starts in a frame and ends by the next).
const transitionAtOnce = 40

// transitionMoves says what the samples of a notice show on its way in (way "in", over ms) or out
// (over ms; it goes when its display turns none or the kernel removes it):
//   - in: "at its end at once" when the first or the next frame already has it whole (opacity 1, no
//     translate); else how its opacity went ("fades in": up, through values between), how its translate
//     went ("in place": never; "slides up from below": from a positive y down to none, through values
//     between; "slides down from above": from a negative y), and when it reached its end;
//   - out: "display none at once" or "removed at once"; "invisible at once" when it is drawn with no
//     opacity until it goes; else "fades out", then "in place", "slides down" (toward a positive y) or
//     "slides up", and until when it was shown.
//
// Anything else is said as it is (back and forth, jumps, never), so a failure reads what went wrong.
func transitionMoves(samples []transitionSample, way string, ms float64) string {
	if len(samples) == 0 {
		return "never read"
	}
	shown := func(s transitionSample) bool { return !s.Gone && s.Display != "none" }
	first := samples[0]
	// end is where the last run of samples that pass begins, -1 when the last sample does not pass
	end := func(pass func(transitionSample) bool) int {
		at := -1
		for i, s := range samples {
			switch {
			case !pass(s):
				at = -1
			case at < 0:
				at = i
			}
		}
		return at
	}
	// rising: value never falls from one sample to the next; between: some sample is strictly inside
	rising := func(list []transitionSample, value func(transitionSample) float64) bool {
		for i := 1; i < len(list); i++ {
			if value(list[i]) < value(list[i-1])-0.001 {
				return false
			}
		}
		return true
	}
	between := func(list []transitionSample, value func(transitionSample) float64, low, high float64) bool {
		for _, s := range list {
			if v := value(s); v > low && v < high {
				return true
			}
		}
		return false
	}
	opacity := func(s transitionSample) float64 { return s.Opacity }
	away := func(s transitionSample) float64 { return math.Abs(s.Y) }
	farthest := 0.0
	for _, s := range samples {
		if shown(s) {
			farthest = math.Max(farthest, math.Abs(s.Y))
		}
	}
	if way == "in" {
		if !shown(first) {
			return "not shown"
		}
		settled := end(func(s transitionSample) bool { return shown(s) && s.Opacity >= 0.999 && math.Abs(s.Y) < 0.05 })
		switch {
		case settled < 0:
			return "never at its end"
		case samples[settled].T-first.T <= transitionAtOnce:
			return "at its end at once"
		}
		moving := samples[:settled+1]
		var words []string
		switch {
		case !rising(moving, opacity):
			words = append(words, "opacity back and forth")
		case first.Opacity >= 0.999:
			words = append(words, "opacity 1 throughout")
		case between(moving, opacity, 0.05, 0.95):
			words = append(words, "fades in")
		default:
			words = append(words, "opacity jumps")
		}
		nearer := func(s transitionSample) float64 { return -math.Abs(s.Y) } // toward none, never away
		switch {
		case farthest < 0.05:
			words = append(words, "in place")
		case !rising(moving, nearer) || math.Abs(first.Y) < farthest-0.05:
			words = append(words, "translate back and forth")
		case !between(moving, away, 0.5, farthest-0.5):
			words = append(words, "translate jumps")
		case first.Y > 0:
			words = append(words, "slides up from below")
		default:
			words = append(words, "slides down from above")
		}
		return strings.Join(append(words, "settled "+transitionAround(samples[settled].T, ms)), ", ")
	}
	gone := end(func(s transitionSample) bool { return !shown(s) })
	if gone < 0 {
		return "never goes"
	}
	how := "display none"
	if samples[gone].Gone {
		how = "removed"
	}
	if samples[gone].T-first.T <= transitionAtOnce {
		return how + " at once"
	}
	visible := samples[:gone]
	invisible := true
	for _, s := range visible {
		invisible = invisible && s.Opacity < 0.01
	}
	if invisible {
		return "invisible at once, " + how + " " + transitionAround(samples[gone].T, ms)
	}
	var words []string
	switch {
	case !rising(visible, func(s transitionSample) float64 { return -s.Opacity }):
		words = append(words, "opacity back and forth")
	case visible[len(visible)-1].Opacity >= 0.999:
		words = append(words, "opacity 1 throughout")
	case between(visible, opacity, 0.05, 0.95):
		words = append(words, "fades out")
	default:
		words = append(words, "opacity jumps")
	}
	last := visible[len(visible)-1]
	switch {
	case farthest < 0.05:
		words = append(words, "in place")
	case !rising(visible, away):
		words = append(words, "translate back and forth")
	case !between(visible, away, 0.5, farthest-0.5):
		words = append(words, "translate jumps")
	case last.Y > 0:
		words = append(words, "slides down")
	default:
		words = append(words, "slides up")
	}
	return strings.Join(append(words, "shown until "+transitionAround(samples[gone].T, ms)+", then "+how), ", ")
}

// transitionWant is what a notice must show one way, as "<transitions> | <moves>": the transitions the
// browser runs on it (transitionList) and what its samples read (transitionMoves), with motion allowed
// and with less motion asked for (the reboot's reduced motion: each transition 0.01ms long).
type transitionWant struct{ moving, reduced string }

// What each notice must show, in (200ms) and out (150ms). The toast's notice leaves by data-leaving and
// the kernel removes it after 150ms (at once with less motion asked for: nothing is left to move); a
// network's notice leaves by show (hidden) and keeps its display until its transitions end (display …
// allow-discrete). A slide comes from the edge its place names and goes back to it (a bottom place:
// positive y, half a rem; a top place: negative); fade never moves; none is at its end at once.
var (
	transitionSlideInBelow = transitionWant{
		"opacity 0→1 200ms, translate 0px 8px→none 200ms | fades in, slides up from below, settled ~200ms",
		"opacity 0→1 0.01ms, translate 0px 8px→none 0.01ms | at its end at once"}
	transitionSlideInAbove = transitionWant{
		"opacity 0→1 200ms, translate 0px -8px→none 200ms | fades in, slides down from above, settled ~200ms",
		"opacity 0→1 0.01ms, translate 0px -8px→none 0.01ms | at its end at once"}
	transitionFadeIn = transitionWant{
		"opacity 0→1 200ms | fades in, in place, settled ~200ms",
		"opacity 0→1 0.01ms | at its end at once"}
	transitionNoneIn = transitionWant{
		"no transition | at its end at once",
		"no transition | at its end at once"}

	transitionToastSlideOutDown = transitionWant{
		"opacity 1→0 150ms, translate none→0px 8px 150ms | fades out, slides down, shown until ~150ms, then removed",
		"no transition | removed at once"}
	transitionToastSlideOutUp = transitionWant{
		"opacity 1→0 150ms, translate none→0px -8px 150ms | fades out, slides up, shown until ~150ms, then removed",
		"no transition | removed at once"}
	transitionToastFadeOut = transitionWant{
		"opacity 1→0 150ms | fades out, in place, shown until ~150ms, then removed",
		"no transition | removed at once"}
	transitionToastNoneOut = transitionWant{
		"no transition | removed at once", // the toast removes it at once: no 150ms of an empty row
		"no transition | removed at once"}

	transitionNetworkSlideOutDown = transitionWant{
		"display flex→none 150ms, opacity 1→0 150ms, translate none→0px 8px 150ms | fades out, slides down, shown until ~150ms, then display none",
		"display flex→none 0.01ms, opacity 1→0 0.01ms, translate none→0px 8px 0.01ms | display none at once"}
	transitionNetworkSlideOutUp = transitionWant{
		"display flex→none 150ms, opacity 1→0 150ms, translate none→0px -8px 150ms | fades out, slides up, shown until ~150ms, then display none",
		"display flex→none 0.01ms, opacity 1→0 0.01ms, translate none→0px -8px 0.01ms | display none at once"}
	transitionNetworkFadeOut = transitionWant{
		"display flex→none 150ms, opacity 1→0 150ms | fades out, in place, shown until ~150ms, then display none",
		"display flex→none 0.01ms, opacity 1→0 0.01ms | display none at once"}
	transitionNetworkNoneOut = transitionWant{
		"no transition | display none at once",
		"no transition | display none at once"}
)

// TestBrowserTransition proves transition="slide" | "fade" | "none" on the hosts of the toast and the
// network in a real browser, with motion allowed (prefers-reduced-motion: no-preference, emulated): for
// each of the three, a site writes it on its toast and its placed network (slide twice, so each host
// slides at a bottom place and at a top one), beside two networks in the flow of the page (fade written;
// none, the default). A notice comes in (the toast says one; Drive's kitwork:offline opens every
// network's at once) and goes out (the toast's own dismiss button; each network's close()), and the
// proof samples each notice every frame on both ways:
//   - the hosts carry the classes of their place and transition (slide: none of its own);
//   - slide comes in from the edge its place names, fading, half a rem from it (bottom: positive y, top:
//     negative), and goes back to it; fade never translates; none is at its end at once;
//   - the transitions are 200ms in and 150ms out; a network's notice keeps its display while it goes,
//     until ~150ms, then display none; the toast's leaving notice moves the same way until the kernel
//     removes it, at 150ms;
//   - with prefers-reduced-motion: reduce, the same transitions are 0.01ms long and every notice is at
//     its end, gone or removed at once (by the next frame).
func TestBrowserTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the transition browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	faded := [2]transitionWant{transitionFadeIn, transitionNetworkFadeOut}
	plain := [2]transitionWant{transitionNoneIn, transitionNetworkNoneOut}
	for _, site := range []struct {
		name, transition, toastPlace, networkPlace string
		hosts                                      string
		wants                                      map[string][2]transitionWant // notice: in, out
	}{
		{"slide, toast below, network above", "slide", "bottom-end", "top-start",
			"toast toast-bottom-end ; network network-top-start ; transition-fade ; -",
			map[string][2]transitionWant{
				"toast":  {transitionSlideInBelow, transitionToastSlideOutDown},
				"placed": {transitionSlideInAbove, transitionNetworkSlideOutUp},
				"faded":  faded, "plain": plain,
			}},
		{"slide, toast above, network below", "slide", "top-end", "bottom-start",
			"toast toast-top-end ; network network-bottom-start ; transition-fade ; -",
			map[string][2]transitionWant{
				"toast":  {transitionSlideInAbove, transitionToastSlideOutUp},
				"placed": {transitionSlideInBelow, transitionNetworkSlideOutDown},
				"faded":  faded, "plain": plain,
			}},
		{"fade", "fade", "top-end", "bottom-start",
			"toast toast-top-end transition-fade ; network network-bottom-start transition-fade ; transition-fade ; -",
			map[string][2]transitionWant{
				"toast":  {transitionFadeIn, transitionToastFadeOut},
				"placed": {transitionFadeIn, transitionNetworkFadeOut},
				"faded":  faded, "plain": plain,
			}},
		{"none", "none", "bottom-end", "top-start",
			"toast toast-bottom-end transition-none ; network network-top-start transition-none ; transition-fade ; -",
			map[string][2]transitionWant{
				"toast":  {transitionNoneIn, transitionToastNoneOut},
				"placed": {transitionNoneIn, transitionNetworkNoneOut},
				"faded":  faded, "plain": plain,
			}},
	} {
		t.Run(site.name, func(t *testing.T) {
			tenant := buildUISite(t, transitionSite(site.transition, site.toastPlace, site.networkPlace))
			var serving sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				forward := request.Clone(request.Context())
				forward.Host = "localhost"
				serving.Lock()
				defer serving.Unlock()
				tenant.Serve(response, forward)
			}))
			defer server.Close()
			d := openDevtools(t, browser, server.URL+"/", 1280, 800, "")
			d.loaded(exampleSettle)
			d.evaluate(transitionProbe)
			if got := d.evaluate(`window.__motion.hosts()`); got != site.hosts {
				t.Errorf("the hosts' classes:\n got  %s\n want %s", got, site.hosts)
			}
			// in: the toast says a notice that stays; Drive's word opens every network's notice at once.
			// out: the toast's notice by its own dismiss button, each network's by its close().
			come := `kit.scope.$toast.info("Một thông báo.", { duration: 0 }); document.dispatchEvent(new CustomEvent("kitwork:offline"));`
			leave := `document.querySelector('[data-kitwork-component^="toast"] > [data-kit-key] button').click();
  ["placed", "faded", "plain"].forEach(function (id) { document.getElementById("close-" + id).click(); });`
			for _, motion := range []string{"no-preference", "reduce"} {
				d.call("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": motion}}})
				if got := d.evaluate(`String(matchMedia("(prefers-reduced-motion: reduce)").matches)`); got != fmt.Sprint(motion == "reduce") {
					t.Fatalf("prefers-reduced-motion: %s is not emulated (reduce matches: %s)", motion, got)
				}
				in := transitionWatch(t, d, come, transitionNotices...)
				out := transitionWatch(t, d, leave, transitionNotices...)
				for _, name := range transitionNotices {
					for i, way := range []struct {
						name    string
						watched transitionWatched
						ms      float64
					}{{"in", in[name], 200}, {"out", out[name], 150}} {
						want := site.wants[name][i].moving
						if motion == "reduce" {
							want = site.wants[name][i].reduced
						}
						if got := transitionList(way.watched) + " | " + transitionMoves(way.watched.Samples, way.name, way.ms); got != want {
							t.Errorf("%s, %s %s:\n got  %s\n want %s\n samples %s", motion, name, way.name, got, want, transitionTrace(way.watched))
						}
					}
				}
			}
		})
	}
}
