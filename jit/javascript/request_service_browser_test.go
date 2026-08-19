package javascript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestPackageStaticContract(t *testing.T) {
	source := readVanillaFile(t, "service", "request", "1.0.0.js")
	if len(source) == 0 || source[0] != ';' || source[len(source)-1] != '\n' {
		t.Fatal("request@1.0.0 is not a sealable classic script")
	}
	if got := bytes.Count(source, []byte(`kit.service("request"`)); got != 1 {
		t.Fatalf("request@1.0.0 registration count = %d, want 1", got)
	}
	for _, required := range []string{
		`send: send`, `get: get`, `post: post`, `abort: abort`,
		`kit.progress.start(`, `kit.progress.update(`, `kit.progress.finish(`,
		`credentials: "same-origin"`, `mode: "same-origin"`,
	} {
		if !bytes.Contains(source, []byte(required)) {
			t.Fatalf("request@1.0.0 lost %s", required)
		}
	}
	for _, forbidden := range []string{
		`kit.component(`, `XMLHttpRequest`, `localStorage`, `sessionStorage`,
		`global.kit`, `window.kit`, `document.addEventListener`,
	} {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Fatalf("request@1.0.0 contains forbidden coupling %q", forbidden)
		}
	}
}

func TestBuildRequestServiceGraphIsClosedOrderedAndDeterministic(t *testing.T) {
	progress := progressServicePackage(t)
	request := requestServicePackage(t)

	left, err := Build(BuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{request, progress},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := Build(BuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{progress, request},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.Name() != right.Name() || left.SHA256() != right.SHA256() || !bytes.Equal(left.Bytes(), right.Bytes()) {
		t.Fatal("request/progress discovery order changed deterministic graph identity")
	}
	progressAt := bytes.Index(left.Bytes(), progress.Source)
	requestAt := bytes.Index(left.Bytes(), request.Source)
	if progressAt < 0 || requestAt < 0 || progressAt >= requestAt {
		t.Fatalf("request graph order = progress:%d request:%d, want dependency before owner", progressAt, requestAt)
	}

	withoutEdge := request
	withoutEdge.Requires = nil
	withoutDependencyMetadata, err := Build(BuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{progress, withoutEdge},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.SHA256() == withoutDependencyMetadata.SHA256() || bytes.Equal(left.Bytes(), withoutDependencyMetadata.Bytes()) {
		t.Fatal("request-to-progress dependency metadata did not affect graph identity")
	}

	if _, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{request}}); err == nil ||
		!strings.Contains(err.Error(), "requires missing service progress@1.0.0") {
		t.Fatalf("missing progress dependency error = %v", err)
	}
	progressV2 := progress
	progressV2.Version = "2.0.0"
	if _, err := Build(BuildOptions{Profile: ProfileKit, Services: []Service{request, progressV2}}); err == nil ||
		!strings.Contains(err.Error(), "requires service progress@1.0.0 but graph provides 2.0.0") {
		t.Fatalf("mismatched progress dependency error = %v", err)
	}
}

func TestBrowserRequestServiceContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping request service browser contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildRequestServiceArtifact(t, ProfileKit)
	assetPath := "/assets/" + artifact.Name()
	var packageRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/request.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, requestServiceDocument, assetPath)
		case "/service/request/1.0.0.js", "/service/progress/1.0.0.js", "/request.js", "/progress.js":
			packageRequests.Add(1)
			http.Error(response, "packages must already be sealed", http.StatusGone)
		case "/api/json":
			writeRequestJSON(response, http.StatusOK, map[string]any{
				"method": request.Method,
				"kind":   request.URL.Query().Get("kind"),
				"probe":  request.Header.Get("X-Probe"),
			})
		case "/api/echo":
			body, _ := io.ReadAll(request.Body)
			writeRequestJSON(response, http.StatusOK, map[string]any{
				"method":      request.Method,
				"body":        string(body),
				"contentType": request.Header.Get("Content-Type"),
				"csrf":        request.Header.Get("X-CSRF-Token"),
			})
		case "/api/stream":
			body := []byte(`{"stream":true,"value":"known"}`)
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = response.Write(body[:9])
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			_, _ = response.Write(body[9:])
		case "/api/unknown":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"unknown":`))
			if flusher, ok := response.(http.Flusher); ok {
				flusher.Flush()
			}
			_, _ = response.Write([]byte(`true}`))
		case "/api/no-content":
			response.WriteHeader(http.StatusNoContent)
		case "/api/http-error":
			writeRequestJSON(response, http.StatusUnprocessableEntity, map[string]any{"reason": "bad input"})
		case "/api/bad-json":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"broken":`))
		case "/api/too-large":
			response.Header().Set("Content-Type", "text/plain")
			response.Header().Set("Content-Length", strconv.Itoa(8*1024*1024+1))
			response.WriteHeader(http.StatusOK)
		case "/api/network":
			hijacker, ok := response.(http.Hijacker)
			if !ok {
				http.Error(response, "hijacking unavailable", http.StatusInternalServerError)
				return
			}
			connection, _, err := hijacker.Hijack()
			if err == nil {
				_ = connection.Close()
			}
		case "/api/wait":
			select {
			case <-request.Context().Done():
				return
			case <-time.After(2 * time.Second):
				writeRequestJSON(response, http.StatusOK, map[string]any{"waited": true})
			}
		case "/api/fast":
			writeRequestJSON(response, http.StatusOK, map[string]any{"latest": true})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/request.html")
	if got := packageRequests.Load(); got != 0 {
		t.Fatalf("browser fetched an authored service package at runtime %d times", got)
	}
}

func TestBrowserRequestServiceGraphReuseAndIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping request service graph reuse contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	installed := buildRequestServiceArtifact(t, ProfileKit)
	different, err := Build(BuildOptions{
		Profile: ProfileKit,
		Services: []Service{
			progressServicePackage(t),
			requestServicePackage(t),
			{Name: "z-request-probe", Version: "1.0.0", Source: []byte(";kit.service(\"z-request-probe\", {});\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string][]byte{
		"/assets/" + installed.Name(): installed.Bytes(),
		"/assets/" + different.Name(): different.Bytes(),
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if source, ok := assets[request.URL.Path]; ok {
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(source)
			return
		}
		if request.URL.Path == "/reuse.html" {
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, requestGraphReuseDocument, installed.Name(), installed.Name(), different.Name())
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/reuse.html")
}

func TestBrowserRequestCleanupReleasesController(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping request forced-GC cleanup contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildRequestServiceArtifact(t, ProfileKit)
	assetPath := "/assets/" + artifact.Name()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case assetPath:
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(artifact.Bytes())
		case "/retention.html":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(response, requestRetentionDocument, assetPath)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	status, output := runRetentionBrowser(t, browser, server.URL+"/retention.html")
	if status == "unsupported" {
		t.Skipf("browser did not make forced request collection observable\n%s", boundedVanillaOutput(output))
	}
	if status != "passed" {
		t.Fatalf("request cleanup retention contract did not pass\n%s", boundedVanillaOutput(output))
	}
}

func requestServicePackage(t *testing.T) Service {
	t.Helper()
	return Service{
		Name:    "request",
		Version: "1.0.0",
		Requires: []ServiceVersion{{
			Name: "progress", Version: "1.0.0",
		}},
		Source: readVanillaFile(t, "service", "request", "1.0.0.js"),
	}
}

func buildRequestServiceArtifact(t *testing.T, profile Profile) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile:  profile,
		Services: []Service{requestServicePackage(t), progressServicePackage(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func writeRequestJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

const requestServiceDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="csrf-token" content="request-contract-token"><title>Request service contract</title><script>
document.documentElement.setAttribute("data-request-stage", "loading");
globalThis.__requestFetchCalls = [];
globalThis.__requestTimeoutRaces = Object.create(null);
globalThis.__requestRealFetch = globalThis.fetch.bind(globalThis);
globalThis.fetch = function (input, init) {
  var url = new URL(typeof input === "string" ? input : input.url, location.href);
  globalThis.__requestFetchCalls.push({ url: url.href, init: init });
  if (url.pathname === "/api/bad-json") {
    return Promise.resolve(new Response('{"broken":', {
      status: 200,
      headers: { "Content-Type": "application/json", "Content-Length": "10" }
    }));
  }
  if (url.pathname === "/api/too-large") {
    return Promise.resolve(new Response("x", {
      status: 200,
      headers: { "Content-Type": "text/plain", "Content-Length": "8388609" }
    }));
  }
  if (url.pathname === "/api/network") {
    return Promise.reject(new TypeError("synthetic network failure"));
  }
  if (url.pathname === "/api/echo" && init.method === "PUT") {
    var putText = JSON.stringify({ method: "PUT", body: init.body });
    return Promise.resolve(new Response(putText, {
      status: 200,
      headers: { "Content-Type": "application/json", "Content-Length": String(new TextEncoder().encode(putText).byteLength) }
    }));
  }
  if (url.pathname === "/api/no-content") {
    return Promise.resolve(new Response(null, { status: 204 }));
  }
  if (url.pathname === "/api/http-error") {
    var httpText = JSON.stringify({ reason: "bad input" });
    return Promise.resolve(new Response(httpText, {
      status: 422,
      headers: { "Content-Type": "application/json", "Content-Length": String(new TextEncoder().encode(httpText).byteLength) }
    }));
  }
  if (url.pathname === "/api/unknown") {
    return Promise.resolve(new Response('{"unknown":true}', {
      status: 200,
      headers: { "Content-Type": "application/json" }
    }));
  }
  if (url.pathname === "/api/stream") {
    var streamBytes = new TextEncoder().encode('{"stream":true,"value":"known"}');
    var streamBody = new ReadableStream({
      start: function (controller) {
        controller.enqueue(streamBytes.slice(0, 9));
        controller.enqueue(streamBytes.slice(9));
        controller.close();
      }
    });
    return Promise.resolve(new Response(streamBody, {
      status: 200,
      headers: { "Content-Type": "application/json", "Content-Length": String(streamBytes.byteLength) }
    }));
  }
  if (url.pathname === "/api/race-fast" || url.pathname === "/api/fast") {
    return Promise.resolve(new Response('{"latest":true}', {
      status: 200,
      headers: { "Content-Type": "application/json", "Content-Length": "15" }
    }));
  }
  if (url.pathname === "/api/timeout-race") {
    return new Promise(function (_, reject) {
      var name = url.searchParams.get("case");
      function timedOut() {
        globalThis.__requestTimeoutRaces[name] = {
          aborted: true,
          release: function () { reject(new DOMException("delayed timeout abort", "AbortError")); }
        };
      }
      if (init.signal.aborted) timedOut();
      else init.signal.addEventListener("abort", timedOut, { once: true });
    });
  }
  if (url.pathname === "/api/capacity" || url.pathname === "/api/timeout" || url.pathname === "/api/wait") {
    return new Promise(function (_, reject) {
      function cancelled() {
        reject(new DOMException("pending request aborted", "AbortError"));
      }
      if (init.signal.aborted) cancelled();
      else init.signal.addEventListener("abort", cancelled, { once: true });
    });
  }
  return globalThis.__requestRealFetch(input, init);
};
</script><script src=%q></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var nextTurn = __kitTestNextTurn;
  var request = globalThis.kit.request;
  var progress = globalThis.kit.progress;
  var authoredFetches = 0;
  var progressEvents = [];
  var stopProgress = progress.subscribe(function (value) { progressEvents.push(value); });

  function stage(value) { document.documentElement.setAttribute("data-request-stage", value); }

  function capture(run, expectsFetch) {
    if (expectsFetch) authoredFetches++;
    return Promise.resolve().then(run).then(
      function (value) { return { value: value, error: null }; },
      function (error) { return { value: null, error: error }; }
    );
  }
  function result(value, status, label) {
    assert(value && Object.getPrototypeOf(value) === null && Object.isFrozen(value), label + " result was not frozen null-prototype data");
    assert(Object.keys(value).join(",") === "status,url,data", label + " result keys were " + Object.keys(value).join(","));
    assert(value.status === status && typeof value.url === "string" && value.url.indexOf(location.origin + "/") === 0,
      label + " result metadata was invalid");
    return value.data;
  }
  function failure(value, code, label) {
    assert(value && value.name === "KitRequestError" && value.code === code && Object.isFrozen(value),
      label + " error was " + String(value && value.name) + "/" + String(value && value.code));
    assert(Object.keys(value).join(",") === "code,status,url,data", label + " error keys were " + Object.keys(value).join(","));
    assert(typeof value.status === "number" && typeof value.url === "string" && Object.prototype.hasOwnProperty.call(value, "data"),
      label + " error metadata was incomplete");
    return value;
  }
  function validation(value, label) {
    assert(value && value.error instanceof TypeError && value.value === null, label + " did not reject with TypeError");
  }
  function operationEvents(start) {
    return progressEvents.slice(start).filter(function (value) { return value.source === "request"; });
  }
  function outcome(events, expected, label) {
    assert(events.length >= 2 && events[0].phase === "start" && events[events.length - 1].phase === "finish",
      label + " progress did not have start/finish boundaries");
    var id = events[0].id;
    assert(/^request:[1-9][0-9]*$/.test(id), label + " progress id was " + id);
    assert(events.every(function (value) { return value.id === id && value.source === "request"; }),
      label + " progress mixed operation identities");
    assert(events[events.length - 1].outcome === expected, label + " progress outcome was " + events[events.length - 1].outcome);
    return events;
  }

  assert(Object.keys(globalThis.kit).join(",") === "version,component,progress,request",
    "sealed request artifact keys were " + Object.keys(globalThis.kit).join(","));
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(request), "request public namespace was mutable");
  assert(request.version === "1.0.0", "request version was " + request.version);
  assert(Object.keys(request).slice().sort().join(",") === "abort,get,post,send",
    "request namespace members were " + Object.keys(request).join(","));
  assert(globalThis.kit.service === undefined && globalThis.kit.bridge === undefined && request.bridge === undefined &&
    request.fetch === undefined && request.submit === undefined && request.register === undefined,
    "request exposed a registrar, bridge, raw fetch, or removed API");
  assert(globalThis.__requestFetchCalls.length === 0, "request package fetched before an authored call");

	stage("validation");
  var beforeValidation = globalThis.__requestFetchCalls.length;
  validation(await capture(function () { return request.get("https://example.invalid/private"); }, false), "cross-origin URL");
  validation(await capture(function () { return request.get("data:text/plain,private"); }, false), "non-HTTP URL");
  validation(await capture(function () { return request.get(new URL("/api/json", location.href)); }, false), "URL object");
  validation(await capture(function () { return request.get("/api/json", { credentials: "include" }); }, false), "unknown option");
  validation(await capture(function () { return request.get("/api/json", { headers: new Headers() }); }, false), "non-plain headers");
  validation(await capture(function () { return request.get("/api/json", { headers: { "X-Bad": 7 } }); }, false), "non-string header");
  validation(await capture(function () { return request.send("/api/json", { method: "GET", data: {} }); }, false), "GET data");
  validation(await capture(function () { return request.get("/api/json", { method: "POST" }); }, false), "GET method override");
  validation(await capture(function () { return request.post("/api/echo", {}, { data: {} }); }, false), "POST data override");
  validation(await capture(function () { return request.get("/api/json", { timeout: 1.5 }); }, false), "fractional timeout");
  validation(await capture(function () { return request.get("/api/json", { key: "" }); }, false), "empty key");
  validation(await capture(function () { return request.get("/api/json", { key: "x".repeat(129) }); }, false), "oversized key");
  validation(await capture(function () { return request.abort(); }, false), "missing abort key");
  assert(globalThis.__requestFetchCalls.length === beforeValidation, "validation forwarded a request to fetch");

	stage("success");
  var progressAt = progressEvents.length;
	stage("success-get");
  var getAttempt = await capture(function () {
    return request.get("/api/json?kind=get", { headers: { "X-Probe": "plain" } });
  }, true);
  assert(!getAttempt.error, "GET failed: " + String(getAttempt.error));
  var getData = result(getAttempt.value, 200, "GET");
  assert(getData.method === "GET" && getData.kind === "get" && getData.probe === "plain", "GET response JSON was wrong");
  var getFetch = globalThis.__requestFetchCalls[globalThis.__requestFetchCalls.length - 1].init;
  assert(getFetch.method === "GET" && getFetch.credentials === "same-origin" && getFetch.mode === "same-origin" &&
    getFetch.body === undefined && getFetch.headers instanceof Headers, "GET did not use fixed same-origin fetch options");
  outcome(operationEvents(progressAt), "loaded", "GET");

	stage("success-post");
  progressAt = progressEvents.length;
  var postAttempt = await capture(function () {
    return request.post("/api/echo", { answer: 42 }, { key: "post-contract" });
  }, true);
  assert(!postAttempt.error, "POST failed: " + String(postAttempt.error));
  var postData = result(postAttempt.value, 200, "POST");
  assert(postData.method === "POST" && JSON.parse(postData.body).answer === 42 &&
    /^application\/json(?:;|$)/i.test(postData.contentType) && postData.csrf === "request-contract-token",
    "POST JSON or CSRF contract failed");
  assert(request.abort("post-contract") === false, "completed POST retained its active key");
  outcome(operationEvents(progressAt), "loaded", "POST");

	stage("success-put");
  var putAttempt = await capture(function () {
    return request.send("/api/echo", { method: "PUT", data: { value: "sent" } });
  }, true);
  assert(!putAttempt.error && result(putAttempt.value, 200, "send PUT").method === "PUT", "send PUT failed");

	stage("success-empty");
  var emptyAttempt = await capture(function () { return request.get("/api/no-content"); }, true);
  assert(!emptyAttempt.error && result(emptyAttempt.value, 204, "no-content") === null, "204 did not resolve null data");

	stage("success-known-stream");
  progressAt = progressEvents.length;
  var streamAttempt = await capture(function () { return request.get("/api/stream"); }, true);
  assert(!streamAttempt.error && result(streamAttempt.value, 200, "known stream").stream === true, "known stream failed");
  var streamEvents = outcome(operationEvents(progressAt), "loaded", "known stream");
  var measured = streamEvents.filter(function (value) { return value.phase === "progress"; });
  assert(measured.length >= 1 && measured[measured.length - 1].loaded === measured[measured.length - 1].total &&
    measured[measured.length - 1].total > 0, "known Content-Length stream was not measured to completion");

	stage("success-unknown-stream");
  progressAt = progressEvents.length;
  var unknownAttempt = await capture(function () { return request.get("/api/unknown"); }, true);
  assert(!unknownAttempt.error && result(unknownAttempt.value, 200, "unknown stream").unknown === true, "unknown stream failed");
  var unknownEvents = outcome(operationEvents(progressAt), "loaded", "unknown stream");
  assert(unknownEvents[0].total === null && unknownEvents.every(function (value) { return value.phase !== "progress"; }),
    "unknown-length response published false precision");

	stage("failure");
  progressAt = progressEvents.length;
	stage("failure-http");
  var httpAttempt = await capture(function () { return request.get("/api/http-error"); }, true);
  var httpError = failure(httpAttempt.error, "HTTP", "HTTP failure");
  assert(httpError.status === 422 && httpError.data.reason === "bad input", "HTTP failure lost status or parsed body");
  outcome(operationEvents(progressAt), "error", "HTTP failure");

	stage("failure-invalid-json");
  progressAt = progressEvents.length;
  var invalidAttempt = await capture(function () { return request.get("/api/bad-json"); }, true);
  failure(invalidAttempt.error, "INVALID_RESPONSE", "invalid JSON");
  outcome(operationEvents(progressAt), "error", "invalid JSON");

	stage("failure-too-large");
  progressAt = progressEvents.length;
  var largeAttempt = await capture(function () { return request.get("/api/too-large"); }, true);
  failure(largeAttempt.error, "TOO_LARGE", "oversized response");
  outcome(operationEvents(progressAt), "error", "oversized response");

	stage("failure-network");
  progressAt = progressEvents.length;
  var networkAttempt = await capture(function () { return request.get("/api/network"); }, true);
  failure(networkAttempt.error, "NETWORK", "network failure");
  outcome(operationEvents(progressAt), "error", "network failure");

	stage("failure-timeout");
  progressAt = progressEvents.length;
  var timeoutAttempt = await capture(function () {
    return request.get("/api/timeout", { key: "timeout-contract", timeout: 25 });
  }, true);
  failure(timeoutAttempt.error, "TIMEOUT", "timeout");
  assert(request.abort("timeout-contract") === false, "timed-out request retained its active key");
  outcome(operationEvents(progressAt), "error", "timeout");

	stage("failure-timeout-first-cause-abort");
  progressAt = progressEvents.length;
  var timeoutAbortPending = capture(function () {
    return request.get("/api/timeout-race?case=abort", { key: "timeout-race-abort", timeout: 1 });
  }, true);
  await waitFor(function () {
    return globalThis.__requestTimeoutRaces.abort && globalThis.__requestTimeoutRaces.abort.aborted;
  }, "timeout abort race did not reach the delayed fetch rejection gap");
  assert(request.abort("timeout-race-abort") === false, "late abort replaced an established timeout cause");
  globalThis.__requestTimeoutRaces.abort.release();
  failure((await timeoutAbortPending).error, "TIMEOUT", "timeout versus late abort");
  outcome(operationEvents(progressAt), "error", "timeout versus late abort");

	stage("failure-timeout-first-cause-latest");
  var timeoutLatestPending = capture(function () {
    return request.get("/api/timeout-race?case=latest", { key: "timeout-race-latest", timeout: 1 });
  }, true);
  await waitFor(function () {
    return globalThis.__requestTimeoutRaces.latest && globalThis.__requestTimeoutRaces.latest.aborted;
  }, "timeout latest race did not reach the delayed fetch rejection gap");
  progressAt = progressEvents.length;
  var timeoutLatestReplacement = capture(function () {
    return request.get("/api/race-fast", { key: "timeout-race-latest" });
  }, true);
  globalThis.__requestTimeoutRaces.latest.release();
  failure((await timeoutLatestPending).error, "TIMEOUT", "timeout versus latest request");
  var timeoutLatestResult = await timeoutLatestReplacement;
  assert(!timeoutLatestResult.error && result(timeoutLatestResult.value, 200, "timeout latest replacement").latest === true,
    "request after an established timeout did not become the latest owner");
  outcome(operationEvents(progressAt), "loaded", "timeout latest replacement");
  assert(request.abort("timeout-race-latest") === false, "timeout race replacement retained its key");

	stage("cancellation");
  progressAt = progressEvents.length;
  var manualPending = capture(function () {
    return request.get("/api/wait?case=manual", { key: "manual-contract" });
  }, true);
  await nextTurn();
  assert(request.abort("manual-contract") === true, "manual abort did not find the active request");
  failure((await manualPending).error, "CANCELLED", "manual abort");
  assert(request.abort("manual-contract") === false, "manual abort retained its completed key");
  outcome(operationEvents(progressAt), "cancelled", "manual abort");

  var firstPending = capture(function () {
    return request.get("/api/wait?case=latest", { key: "latest-contract" });
  }, true);
  await nextTurn();
  var latestPending = capture(function () {
    return request.get("/api/fast", { key: "latest-contract" });
  }, true);
  failure((await firstPending).error, "CANCELLED", "superseded request");
  var latestAttempt = await latestPending;
  assert(!latestAttempt.error && result(latestAttempt.value, 200, "latest request").latest === true,
    "latest keyed request did not win");
  assert(request.abort("latest-contract") === false, "latest completed request retained its key");

	stage("capacity");
  var capacity = [];
  for (var index = 0; index < 257; index++) {
    (function (entry) {
      capacity.push(capture(function () {
        return request.get("/api/capacity?entry=" + entry, { key: "capacity-" + entry });
      }, true));
    })(index);
  }
  failure((await capacity[0]).error, "CANCELLED", "capacity eviction");
  assert(request.abort("capacity-0") === false, "capacity-evicted request remained active");
  var activeCount = 0;
  for (var activeIndex = 1; activeIndex < 257; activeIndex++) {
    if (request.abort("capacity-" + activeIndex)) activeCount++;
  }
  assert(activeCount === 256, "bounded active ownership retained " + activeCount + " requests instead of 256");
  var capacityResults = await Promise.all(capacity);
  capacityResults.forEach(function (attempt, entry) {
    failure(attempt.error, "CANCELLED", "capacity request " + entry);
  });
  await nextTurn();
  for (var releasedIndex = 0; releasedIndex < 257; releasedIndex++) {
    assert(request.abort("capacity-" + releasedIndex) === false, "settled capacity request " + releasedIndex + " remained active");
  }

  stopProgress();
  assert(globalThis.__requestFetchCalls.length === authoredFetches,
    "request service made " + globalThis.__requestFetchCalls.length + " fetches for " + authoredFetches + " authored requests");
	stage("complete");
});
</script></body></html>`

const requestGraphReuseDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Request graph reuse</title><script>
globalThis.__requestGraphErrors = [];
globalThis.__requestGraphFetches = 0;
globalThis.__requestGraphNavigationAdds = 0;
var __requestGraphFetch = globalThis.fetch;
globalThis.fetch = function () {
  globalThis.__requestGraphFetches++;
  return __requestGraphFetch.apply(this, arguments);
};
var __requestGraphAdd = document.addEventListener.bind(document);
document.addEventListener = function (type, listener, options) {
  if (type === "kit:navigation") globalThis.__requestGraphNavigationAdds++;
  return __requestGraphAdd(type, listener, options);
};
window.addEventListener("error", function (event) {
  var message = String(event.error && event.error.message || event.message || "");
  if (message.indexOf("installed component graph does not match this artifact") >= 0) {
    globalThis.__requestGraphErrors.push(message);
    event.preventDefault();
  }
});
</script><script src="/assets/%s"></script><script>
globalThis.__requestGraphKit = globalThis.kit;
globalThis.__requestGraphRequest = globalThis.kit.request;
globalThis.__requestGraphProgress = globalThis.kit.progress;
</script><script src="/assets/%s"></script><script>
globalThis.__requestGraphSame = globalThis.kit === globalThis.__requestGraphKit &&
  globalThis.kit.request === globalThis.__requestGraphRequest && globalThis.kit.progress === globalThis.__requestGraphProgress;
</script><script src="/assets/%s"></script></head><body><script>
` + browserHarness + `
__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  assert(globalThis.__requestGraphErrors.length === 1, "different request graph did not fail exactly once");
  assert(globalThis.__requestGraphSame && globalThis.kit === globalThis.__requestGraphKit,
    "same or different graph replaced a sealed service facade");
  assert(globalThis.__requestGraphNavigationAdds === 1, "same artifact reran progress package initialization");
  assert(globalThis.__requestGraphFetches === 0, "service graph evaluation performed a fetch");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,progress,request" &&
    globalThis.kit["z-request-probe"] === undefined, "different graph partially registered a service");
  var graph = globalThis.kit[Symbol.for("kitjs:graph")];
  assert(graph && Object.isFrozen(graph) && Object.isFrozen(graph.services) &&
    graph.services.progress === "1.0.0" && graph.services.request === "1.0.0",
    "installed request graph metadata was missing or mutable");
});
</script></body></html>`

const requestRetentionDocument = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<title>Request cleanup retention</title><script>
globalThis.__requestRetentionRealFetch = globalThis.fetch.bind(globalThis);
globalThis.fetch = function (input, init) {
  var url = new URL(typeof input === "string" ? input : input.url, location.href);
  if (url.pathname !== "/api/gc") return globalThis.__requestRetentionRealFetch(input, init);
  globalThis.__requestRetentionSignal = new WeakRef(init.signal);
  return Promise.resolve(new Response('{"collected":true}', {
    status: 200,
    headers: { "Content-Type": "application/json", "Content-Length": "18" }
  }));
};
</script><script src=%q></script></head><body><script>
(function () {
  "use strict";
  var root = document.documentElement;
  function finish(status, error) {
    root.setAttribute("data-kit-retention-test", status);
    if (error) root.setAttribute("data-kit-retention-error", String(error && error.message || error));
  }
  function fail(message) { throw new Error(message); }
  function alive(refs) {
    var count = 0;
    refs.forEach(function (ref) { if (ref.deref() !== undefined) count++; });
    return count;
  }
  function controls() {
    var refs = [];
    for (var index = 0; index < 128; index++) refs.push(new WeakRef({ index: index }));
    return refs;
  }
  function collect(refs, controlRefs, pass) {
    var pressure = [];
    for (var index = 0; index < 8; index++) pressure.push(new Array(65536).fill(pass));
    pressure = null;
    globalThis.gc();
    globalThis.gc();
    if (pass < 7) {
      setTimeout(function () { collect(refs, controlRefs, pass + 1); }, 0);
      return;
    }
    if (alive(controlRefs) !== 0) {
      finish("unsupported", "forced GC retained control objects");
      return;
    }
    if (alive(refs) !== 0) fail("request service retained a completed AbortController signal");
    finish("passed");
  }
  function run() {
    if (typeof WeakRef !== "function" || typeof globalThis.gc !== "function") {
      finish("unsupported", "WeakRef or forced gc() is unavailable");
      return;
    }
    (async function completeRequest() {
      var response = await globalThis.kit.request.get("/api/gc", { key: "gc-contract" });
      if (!response || response.status !== 200 || !response.data || response.data.collected !== true) {
        fail("request retention fixture did not complete");
      }
      if (globalThis.kit.request.abort("gc-contract") !== false) fail("completed request retained its key");
    })().then(function () {
      setTimeout(function () {
        collect([globalThis.__requestRetentionSignal], controls(), 0);
      }, 0);
    }, function (error) { finish("failed", error); });
  }
  window.addEventListener("error", function (event) { finish("failed", event.error || event.message); });
  window.addEventListener("unhandledrejection", function (event) { finish("failed", event.reason); });
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () { setTimeout(run, 0); }, { once: true });
  } else setTimeout(run, 0);
})();
</script></body></html>`
