package vanilla

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrowserDriveNavigationEventLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Drive navigation-event browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact, err := Build(BuildOptions{Profile: ProfileHydrate})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/navigation-events.html":
			writeNavigationHTML(response, navigationEventInitialDocument(assetPath))
		case "/event-loaded":
			writeNavigationHTML(response, navigationRouteDocument(assetPath, "Loaded", "loaded"))
		case "/event-fast":
			writeNavigationHTML(response, navigationRouteDocument(assetPath, "Fast", "fast"))
		case "/event-fallback":
			if request.Header.Get("X-KitJS-Drive") == "1" {
				response.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = response.Write([]byte("not an HTML response"))
				return
			}
			writeNavigationNoContent(response, "kit_navigation_fallback")
		case "/event-error-async":
			writeNavigationNoContent(response, "kit_navigation_async_error")
		case "/event-error-sync":
			writeNavigationNoContent(response, "kit_navigation_sync_error")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/navigation-events.html")
}

func writeNavigationHTML(response http.ResponseWriter, source string) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	_, _ = response.Write([]byte(source))
}

func writeNavigationNoContent(response http.ResponseWriter, cookie string) {
	response.Header().Set("Set-Cookie", cookie+"=1; Path=/; SameSite=Lax")
	response.WriteHeader(http.StatusNoContent)
}

func navigationShell(route string) string {
	return fmt.Sprintf(`<nav aria-label="Navigation event contract">
  <a id="event-loaded" href="/event-loaded">Loaded</a>
  <a id="event-latest-slow" href="/event-latest-slow">Latest slow</a>
  <a id="event-fast" href="/event-fast">Fast</a>
  <a id="event-pagehide" href="/event-pagehide">Pagehide</a>
  <a id="event-fallback" href="/event-fallback">Fallback</a>
  <a id="event-error-async" href="/event-error-async">Async error</a>
  <a id="event-error-sync" href="/event-error-sync">Sync error</a>
</nav>
<main id="navigation-route" data-route=%q>%s</main>`, route, route)
}

func navigationRouteDocument(assetPath, title, route string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>%s</title><script src=%q></script></head>
<body>%s</body></html>`, title, assetPath, navigationShell(route))
}

func navigationEventInitialDocument(assetPath string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Navigation events</title>
<script>%s</script><script src=%q></script></head>
<body>%s<script>
%s
%s
</script></body></html>`, navigationEventRecorder, assetPath, navigationShell("initial"),
		browserHarness, navigationEventLifecycleAssertions)
}

const navigationEventRecorder = `(function () {
  "use strict";
  var state = globalThis.__navigationContract = { events: [], errors: [] };
  function reject(message) { state.errors.push(message); }
  function primitive(value) {
    return value !== null && (typeof value === "string" || typeof value === "number" ||
      typeof value === "boolean");
  }
  document.addEventListener("kit:navigation", function (event) {
    var detail = event.detail;
    var index = state.events.length;
    if (!detail || typeof detail !== "object") {
      reject("event " + index + " has no detail object");
      return;
    }
    if (!Object.isFrozen(detail) || Object.isExtensible(detail)) {
      reject("event " + index + " detail is mutable");
    }
    var expected = detail.phase === "start" ? "id,phase,url" :
      detail.phase === "progress" ? "id,loaded,phase,total,url" :
      detail.phase === "finish" ? "id,outcome,phase,url" : "";
    var keys = Object.keys(detail).sort().join(",");
    if (keys !== expected) reject("event " + index + " keys were " + keys);
    Object.keys(detail).forEach(function (key) {
      if (!primitive(detail[key])) reject("event " + index + " field " + key + " was not primitive");
    });
    if (!Number.isSafeInteger(detail.id) || detail.id < 1) reject("event " + index + " id was invalid");
    if (typeof detail.url !== "string" || !detail.url) reject("event " + index + " url was invalid");
    if (detail.phase === "progress" &&
      (!Number.isSafeInteger(detail.loaded) || !Number.isSafeInteger(detail.total) ||
        detail.loaded < 0 || detail.total < 1 || detail.loaded > detail.total)) {
      reject("event " + index + " progress was invalid");
    }
    if (detail.phase === "finish" &&
      ["loaded", "cancelled", "error", "fallback"].indexOf(detail.outcome) < 0) {
      reject("event " + index + " outcome was invalid");
    }
    var phase = detail.phase;
    try { detail.phase = "mutated"; detail.extra = true; } catch (_) { /* Frozen by contract. */ }
    if (detail.phase !== phase || Object.prototype.hasOwnProperty.call(detail, "extra")) {
      reject("event " + index + " detail accepted a mutation");
    }
    state.events.push({
      id: detail.id,
      phase: detail.phase,
      url: detail.url,
      loaded: detail.loaded,
      total: detail.total,
      outcome: detail.outcome,
      path: location.pathname,
      route: document.getElementById("navigation-route") &&
        document.getElementById("navigation-route").getAttribute("data-route")
    });
    document.documentElement.setAttribute("data-kit-navigation-events", JSON.stringify(state.events));
  });
})();`

const navigationEventLifecycleAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var state = globalThis.__navigationContract;
  var realFetch = globalThis.fetch.bind(globalThis);

  globalThis.fetch = function (source, options) {
    var path = new URL(String(source), location.href).pathname;
    if (path === "/event-error-sync") throw new Error("synchronous fetch failure");
    if (path === "/event-error-async") return Promise.reject(new Error("asynchronous fetch failure"));
    if (path === "/event-latest-slow" || path === "/event-pagehide") {
      return new Promise(function (_, reject) {
        if (options && options.signal) options.signal.addEventListener("abort", function () {
          reject(new DOMException("Aborted", "AbortError"));
        }, { once: true });
      });
    }
    return realFetch(source, options);
  };

  function pathOf(record) { return new URL(record.url).pathname; }
  function newestStart(path, after) {
    for (var index = state.events.length - 1; index >= after; index--) {
      var event = state.events[index];
      if (event.phase === "start" && pathOf(event) === path) return event;
    }
    return null;
  }
  async function start(id, path) {
    var after = state.events.length;
    document.getElementById(id).click();
    await waitFor(function () { return !!newestStart(path, after); }, path + " did not emit start");
    return newestStart(path, after);
  }
  function finish(id) {
    return state.events.find(function (event) { return event.id === id && event.phase === "finish"; });
  }
  async function expectFinish(started, outcome, message) {
    await waitFor(function () {
      var ended = finish(started.id);
      return !!ended && ended.outcome === outcome;
    }, message);
    return finish(started.id);
  }
  function indexOf(id, phase) {
    return state.events.findIndex(function (event) { return event.id === id && event.phase === phase; });
  }
  function snapshot() {
    return {
      root: document.documentElement,
      body: document.body,
      title: document.title,
      path: location.pathname,
      route: document.getElementById("navigation-route"),
      routeText: document.getElementById("navigation-route").textContent
    };
  }
  function unchanged(before, label) {
    assert(document.documentElement === before.root, label + " replaced the document root");
    assert(document.body === before.body, label + " replaced the body");
    assert(document.title === before.title && location.pathname === before.path,
      label + " changed title or URL before hard fallback");
    assert(document.getElementById("navigation-route") === before.route &&
      before.route.textContent === before.routeText, label + " mutated body before hard fallback");
  }

  var loaded = await start("event-loaded", "/event-loaded");
  var loadedFinish = await expectFinish(loaded, "loaded", "normal navigation did not finish loaded");
  assert(location.pathname === "/event-loaded" && loadedFinish.path === "/event-loaded" &&
    loadedFinish.route === "loaded", "loaded finish was emitted before commit");

  var slow = await start("event-latest-slow", "/event-latest-slow");
  var fast = await start("event-fast", "/event-fast");
  await expectFinish(slow, "cancelled", "superseded navigation did not finish cancelled");
  await expectFinish(fast, "loaded", "latest navigation did not finish loaded");
  assert(indexOf(slow.id, "finish") < indexOf(fast.id, "start"),
    "superseded finish was not emitted before the latest start");
  assert(location.pathname === "/event-fast" &&
    document.getElementById("navigation-route").getAttribute("data-route") === "fast",
    "latest navigation did not win");

  var pagehide = await start("event-pagehide", "/event-pagehide");
  globalThis.dispatchEvent(new Event("pagehide"));
  await expectFinish(pagehide, "cancelled", "pagehide did not finish the active visit as cancelled");
  assert(location.pathname === "/event-fast", "synthetic pagehide committed a pending visit");

  var stable = snapshot();
  var fallback = await start("event-fallback", "/event-fallback");
  await expectFinish(fallback, "fallback", "non-HTML response did not finish as fallback");
  await waitFor(function () { return document.cookie.indexOf("kit_navigation_fallback=1") >= 0; },
    "non-HTML response did not hard-navigate after fallback");
  unchanged(stable, "non-HTML fallback");

  stable = snapshot();
  var asyncError = await start("event-error-async", "/event-error-async");
  await expectFinish(asyncError, "error", "rejected fetch did not finish as error");
  await waitFor(function () { return document.cookie.indexOf("kit_navigation_async_error=1") >= 0; },
    "rejected fetch did not hard-navigate after error");
  unchanged(stable, "rejected fetch");

  stable = snapshot();
  var syncError = await start("event-error-sync", "/event-error-sync");
  await expectFinish(syncError, "error", "synchronous fetch throw did not finish as error");
  await waitFor(function () { return document.cookie.indexOf("kit_navigation_sync_error=1") >= 0; },
    "synchronous fetch throw did not hard-navigate after error");
  unchanged(stable, "synchronous fetch throw");
  await nextTurn();

  assert(state.errors.length === 0, "navigation detail contract errors: " + state.errors.join("; "));
  var starts = state.events.filter(function (event) { return event.phase === "start"; });
  assert(starts.length === 7, "expected seven starts, got " + starts.length);
  starts.forEach(function (started) {
    var records = state.events.filter(function (event) { return event.id === started.id; });
    var finishes = records.filter(function (event) { return event.phase === "finish"; });
    assert(records[0].phase === "start", "visit " + started.id + " did not begin with start");
    assert(finishes.length === 1, "visit " + started.id + " emitted " + finishes.length + " finishes");
    assert(records[records.length - 1].phase === "finish",
      "visit " + started.id + " emitted an event after finish");
  });
});`

func TestBrowserDriveNavigationStreamProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Drive streaming-progress browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact, err := Build(BuildOptions{Profile: ProfileHydrate})
	if err != nil {
		t.Fatal(err)
	}
	assetPath := "/assets/" + artifact.Name()
	known := navigationStreamDocument(assetPath, "Known stream", "known", 64<<10)
	unknown := navigationStreamDocument(assetPath, "Unknown stream", "unknown", 32<<10)
	encoded := navigationStreamDocument(assetPath, "Encoded stream", "encoded", 32<<10)
	knownTotal := len([]byte(known))

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/navigation-progress.html":
			writeNavigationHTML(response, navigationProgressInitialDocument(assetPath, knownTotal))
		case "/stream-known":
			writeNavigationStream(response, request, []byte(known), true, false, true)
		case "/stream-unknown":
			writeNavigationStream(response, request, []byte(unknown), false, false, false)
		case "/stream-encoded":
			writeNavigationStream(response, request, []byte(encoded), true, true, false)
		case "/stream-fast":
			writeNavigationHTML(response, navigationStreamDocument(assetPath, "Stream fast", "fast", 0))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/navigation-progress.html")
}

func navigationProgressShell(route string) string {
	return fmt.Sprintf(`<nav aria-label="Navigation progress contract">
  <a id="stream-known" href="/stream-known">Known</a>
  <a id="stream-unknown" href="/stream-unknown">Unknown</a>
  <a id="stream-encoded" href="/stream-encoded">Encoded</a>
  <a id="stream-fast" href="/stream-fast">Fast</a>
</nav><main id="navigation-route" data-route=%q>%s</main>`, route, route)
}

func navigationStreamDocument(assetPath, title, route string, padding int) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>%s</title><script src=%q></script></head>
<body>%s<div hidden data-stream-padding>%s</div></body></html>`, title, assetPath,
		navigationProgressShell(route), strings.Repeat("x", padding))
}

func navigationProgressInitialDocument(assetPath string, knownTotal int) string {
	assertions := fmt.Sprintf(navigationStreamAssertionsFormat, knownTotal)
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Navigation progress</title>
<script>%s</script><script src=%q></script></head>
<body>%s<script>
%s
%s
</script></body></html>`, navigationEventRecorder, assetPath, navigationProgressShell("initial"),
		browserHarness, assertions)
}

func writeNavigationStream(response http.ResponseWriter, request *http.Request, source []byte, exactLength, encoded, holdAfterFirst bool) {
	payload := source
	if encoded {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, _ = writer.Write(source)
		_ = writer.Close()
		payload = compressed.Bytes()
		response.Header().Set("Content-Encoding", "gzip")
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	if exactLength {
		response.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
	}
	response.WriteHeader(http.StatusOK)
	flusher, _ := response.(http.Flusher)
	// Keep a real response body pending before its first measured chunk. Chrome's
	// virtual-time browser runner then observes the flushed byte boundary instead
	// of racing a wall-clock sleep after progress has already fired.
	if exactLength && !encoded {
		select {
		case <-request.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	first := len(payload) / 3
	if first < 1 {
		first = len(payload)
	}
	if _, err := response.Write(payload[:first]); err != nil {
		return
	}
	if flusher != nil {
		flusher.Flush()
	}
	if holdAfterFirst {
		select {
		case <-request.Context().Done():
			return
		case <-time.After(750 * time.Millisecond):
		}
	}
	if first < len(payload) {
		_, _ = response.Write(payload[first:])
	}
}

const navigationStreamAssertionsFormat = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var state = globalThis.__navigationContract;

  function pathOf(record) { return new URL(record.url).pathname; }
  async function start(id, path) {
    var after = state.events.length;
    document.getElementById(id).click();
    await waitFor(function () {
      return state.events.slice(after).some(function (event) {
        return event.phase === "start" && pathOf(event) === path;
      });
    }, path + " did not emit start");
    return state.events.slice(after).find(function (event) {
      return event.phase === "start" && pathOf(event) === path;
    });
  }
  function records(id) {
    return state.events.filter(function (event) { return event.id === id; });
  }
  function finish(id) {
    return records(id).find(function (event) { return event.phase === "finish"; });
  }
  async function loaded(started, route) {
    await waitFor(function () {
      var ended = finish(started.id);
      return !!ended && ended.outcome === "loaded" && location.pathname === new URL(started.url).pathname &&
        document.getElementById("navigation-route").getAttribute("data-route") === route;
    }, started.url + " did not finish loaded");
  }
  function progress(id) {
    return records(id).filter(function (event) { return event.phase === "progress"; });
  }
  function assertMonotonic(started, expectedTotal) {
    var values = progress(started.id);
    assert(values.length > 0, pathOf(started) + " emitted no measured progress");
    assert(values.length <= 99, pathOf(started) + " emitted more than 99 progress events");
    var prior = 0;
    values.forEach(function (event) {
      assert(event.loaded > prior, pathOf(started) + " progress did not increase");
      assert(event.loaded <= event.total, pathOf(started) + " loaded exceeded total");
      assert(Math.floor(event.loaded * 100 / event.total) <= 99,
        pathOf(started) + " emitted progress at or above 100 percent");
      if (expectedTotal) assert(event.total === expectedTotal,
        pathOf(started) + " exposed a false total " + event.total);
      prior = event.loaded;
    });
  }

  var known = await start("stream-known", "/stream-known");
  await waitFor(function () { return progress(known.id).length > 0; },
    "known-length stream emitted no measured progress");
  assertMonotonic(known, %d);

  var fast = await start("stream-fast", "/stream-fast");
  await waitFor(function () {
    var ended = finish(known.id);
    return !!ended && ended.outcome === "cancelled";
  }, "superseded measured stream did not finish cancelled");
  await loaded(fast, "fast");
  var knownFinish = state.events.findIndex(function (event) {
    return event.id === known.id && event.phase === "finish";
  });
  var fastStart = state.events.findIndex(function (event) {
    return event.id === fast.id && event.phase === "start";
  });
  assert(knownFinish < fastStart, "superseded finish did not precede latest start");
  assert(!state.events.slice(fastStart).some(function (event) {
    return event.id === known.id && event.phase === "progress";
  }), "superseded visit emitted progress after the latest visit started");

  var unknown = await start("stream-unknown", "/stream-unknown");
  await loaded(unknown, "unknown");
  assert(progress(unknown.id).length === 0, "unknown-length response invented determinate progress");

  var encoded = await start("stream-encoded", "/stream-encoded");
  await loaded(encoded, "encoded");
  assert(progress(encoded.id).length === 0, "encoded response invented determinate progress");
  await nextTurn();

  assert(state.errors.length === 0, "navigation progress detail errors: " + state.errors.join("; "));
  var starts = state.events.filter(function (event) { return event.phase === "start"; });
  assert(starts.length === 4, "expected four progress-fixture starts, got " + starts.length);
  starts.forEach(function (started) {
    var visit = records(started.id);
    assert(visit[0].phase === "start", "visit " + started.id + " did not start first");
    assert(visit.filter(function (event) { return event.phase === "finish"; }).length === 1,
      "visit " + started.id + " did not finish exactly once");
    assert(visit[visit.length - 1].phase === "finish", "visit " + started.id + " emitted after finish");
  });
});`
