package javascript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const requestFormArtifactName = "hydrate.kit.0.9.0-next.12.f7b86e4c317e18003127ebb77e3f86e218a96d4133439cbaf696d93260be710c.js"

var requestFormProgressHostRE = regexp.MustCompile(`(?is)<section\b[^>]*\bdata-kit-retain\s*=\s*"request-progress"[^>]*>`)

func TestRequestFormExampleContract(t *testing.T) {
	artifact := buildRequestFormArtifact(t)
	if artifact.Name() != requestFormArtifactName {
		t.Fatalf("request form artifact = %q, want %q", artifact.Name(), requestFormArtifactName)
	}
	checked := readVanillaFile(t, "examples", "request-form", requestFormArtifactName)
	if !bytes.Equal(checked, artifact.Bytes()) {
		t.Fatalf("checked request form artifact %s is stale", requestFormArtifactName)
	}
	if ContentHash(checked) != artifact.SHA256() || !strings.Contains(requestFormArtifactName, artifact.SHA256()) {
		t.Fatal("checked request form artifact filename does not identify its exact bytes")
	}

	var retainedAttributes map[string]string
	for _, name := range []string{"index.html", "design.html"} {
		source := string(readVanillaFile(t, "examples", "request-form", name))
		matches := externalScriptRE.FindAllStringSubmatch(source, -1)
		if len(matches) != 1 {
			t.Fatalf("request-form/%s external script count = %d, want one", name, len(matches))
		}
		script := matches[0][1]
		if script == "" {
			script = matches[0][2]
		}
		if script != "./"+requestFormArtifactName {
			t.Fatalf("request-form/%s artifact URL = %q, want %q", name, script, "./"+requestFormArtifactName)
		}
		stableTag := `<script defer src="./` + requestFormArtifactName + `" data-kit-drive="stable"></script>`
		if strings.Count(source, stableTag) != 1 || strings.Count(source, `data-kit-drive="stable"`) != 1 {
			t.Fatalf("request-form/%s must contain exactly one stable Hydrate artifact tag", name)
		}
		for _, required := range []string{
			`data-kit-retain="request-progress"`, `data-kit-component="progress-bar"`,
			`data-kit-version="2.0.0"`, `data-kit-show="visible"`, `role="progressbar"`, `max-w-8xl`,
			`class="pointer-events-none fixed inset-x-0 top-0 z-50 h-1"`,
			`id="request-progress-label" class="sr-only"`,
			`class="h-1 w-full overflow-hidden bg-slate-800"`,
			`focusable="false"`, `class="block h-1 w-full"`,
			`focus-visible:outline`, `href="../` + exampleStylesheetName + `"`,
		} {
			if !strings.Contains(source, required) {
				t.Fatalf("request-form/%s lost %s", name, required)
			}
		}
		for _, forbidden := range []string{
			"__SHA256__", "data-kit-app", "data-kit-hydrate", "data-kit-style", "<style", "<script type=\"module\"",
			`data-kit-component="progress-bar@`, `data-kit-component="request-form@`, `data-kit-target`,
		} {
			if strings.Contains(strings.ToLower(source), strings.ToLower(forbidden)) {
				t.Fatalf("request-form/%s contains forbidden construct %q", name, forbidden)
			}
		}
		host := requestFormProgressHostRE.FindString(source)
		if host == "" {
			t.Fatalf("request-form/%s has no retained progress host", name)
		}
		if !strings.Contains(strings.ToLower(host), " hidden") {
			t.Fatalf("request-form/%s progress host is not authored hidden", name)
		}
		attributes := make(map[string]string)
		for _, match := range shopDataKitAttrRE.FindAllStringSubmatch(host, -1) {
			attributes[strings.ToLower(match[1])] = match[2]
		}
		if retainedAttributes == nil {
			retainedAttributes = attributes
		} else if !sameStringMap(retainedAttributes, attributes) {
			t.Fatalf("request-form/%s retained attributes = %#v, want %#v", name, attributes, retainedAttributes)
		}
	}

	index := string(readVanillaFile(t, "examples", "request-form", "index.html"))
	for _, required := range []string{
		`data-kit-component="request-form"`, `data-kit-version="1.0.0"`,
		`data-kit-submit:prevent="save()"`, `data-kit-model="name"`, `data-kit-model="email"`,
		`id="request-profile-save"`, `id="request-profile-latest"`, `id="request-profile-fail"`,
		`id="request-profile-cancel"`,
	} {
		if !strings.Contains(index, required) {
			t.Fatalf("request form index lost %s", required)
		}
	}
	if strings.Contains(string(readVanillaFile(t, "examples", "request-form", "design.html")),
		`data-kit-component="request-form"`) {
		t.Fatal("request form design route unexpectedly mounts the form component")
	}

	component := readVanillaFile(t, "examples", "request-form", "request-form.js")
	if got := bytes.Count(component, []byte(`kit.component("request-form"`)); got != 1 {
		t.Fatalf("request-form@1.0.0 registration count = %d, want one", got)
	}
	for _, required := range []string{`kit.request.post(`, `kit.request.abort(requestKey)`, `return function ()`} {
		if !bytes.Contains(component, []byte(required)) {
			t.Fatalf("request-form@1.0.0 lost %s", required)
		}
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "document.", `kit.service(`, "localStorage"} {
		if bytes.Contains(component, []byte(forbidden)) {
			t.Fatalf("request-form@1.0.0 bypasses its service boundary with %q", forbidden)
		}
	}
}

func TestBrowserRequestFormExample(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping request form browser acceptance contract in short mode")
	}
	browser := findVanillaBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	artifact := buildRequestFormArtifact(t)
	assetPath := "/examples/request-form/" + artifact.Name()
	stylePath := "/examples/" + exampleStylesheetName
	stylesheet := readVanillaFile(t, "examples", exampleStylesheetName)
	fetchSpySource := []byte(requestFormFetchSpySource)
	contractSource := []byte(requestFormBrowserContractSource)
	directContractSource := []byte(requestFormDirectBrowserContractSource)
	driveScriptTags := requestFormDriveScriptTags(
		artifact.Name(),
		driveScriptIntegrity(fetchSpySource),
		driveScriptIntegrity(contractSource),
	)
	directScriptTags := requestFormDirectScriptTags(
		artifact.Name(),
		driveScriptIntegrity(directContractSource),
	)
	pages := map[string][]byte{
		"/examples/request-form/index.html":  requestFormPage(t, "index.html", artifact.Name(), driveScriptTags),
		"/examples/request-form/design.html": requestFormPage(t, "design.html", artifact.Name(), driveScriptTags),
	}
	initial := pages["/examples/request-form/index.html"]
	directDesign := requestFormPage(t, "design.html", artifact.Name(), directScriptTags)

	var artifactRequests atomic.Int64
	var styleRequests atomic.Int64
	state := &requestFormAPIState{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/examples/request-form/request-form-fetch-spy.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(fetchSpySource)
			return
		case assetPath:
			artifactRequests.Add(1)
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = response.Write(artifact.Bytes())
			return
		case "/examples/request-form/request-form-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(contractSource)
			return
		case "/examples/request-form/request-form-direct-contract.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(directContractSource)
			return
		case stylePath:
			styleRequests.Add(1)
			response.Header().Set("Content-Type", "text/css; charset=utf-8")
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			_, _ = response.Write(stylesheet)
			return
		case "/api/profile":
			handleRequestFormAPI(response, request, state)
			return
		}

		page, ok := pages[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}
		if request.URL.Query().Get("direct") == "1" && request.URL.Path == "/examples/request-form/design.html" {
			writeRequestFormHTML(response, directDesign)
			return
		}
		if request.Header.Get("X-KitJS-Drive") != "1" &&
			request.URL.Path == "/examples/request-form/index.html" {
			writeRequestFormHTML(response, initial)
			return
		}
		writeRequestFormHTML(response, page)
	}))
	defer server.Close()

	runVanillaBrowser(t, browser, server.URL+"/examples/request-form/index.html")
	if got := artifactRequests.Load(); got != 1 {
		t.Fatalf("request form navigation fetched its sealed runtime %d times, want 1", got)
	}
	assertRequestFormSubmissions(t, state.snapshot())

	runVanillaBrowser(t, browser, server.URL+"/examples/request-form/design.html?direct=1")
	if got := artifactRequests.Load(); got != 2 {
		t.Fatalf("request form artifact requests after direct design load = %d, want 2", got)
	}
	if got := styleRequests.Load(); got != 2 {
		t.Fatalf("request form stylesheet requests = %d, want 2", got)
	}
}

func buildRequestFormArtifact(t *testing.T) Artifact {
	t.Helper()
	artifact, err := Build(BuildOptions{
		Profile:  ProfileHydrate,
		Services: []Service{requestServicePackage(t), progressServicePackage(t)},
		Components: []ComponentVersion{
			{Name: "progress-bar", Version: "2.0.0"},
			{Name: "request-form", Version: "1.0.0"},
		},
		ComponentRequires: []ComponentServiceRequirement{
			{Component: "progress-bar", Service: ServiceVersion{Name: "progress", Version: "1.0.0"}},
			{Component: "request-form", Service: ServiceVersion{Name: "request", Version: "1.0.0"}},
		},
		Scripts: []Script{
			{Name: "progress-bar", Source: readVanillaFile(t, "component", "progress-bar", "2.0.0.js")},
			{Name: "request-form", Source: readVanillaFile(t, "examples", "request-form", "request-form.js")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func requestFormPage(t *testing.T, name, artifactName, scriptTags string) []byte {
	t.Helper()
	source := readVanillaFile(t, "examples", "request-form", name)
	matches := externalScriptRE.FindAllStringSubmatch(string(source), -1)
	if len(matches) != 1 {
		t.Fatalf("request-form/%s external script count = %d, want one", name, len(matches))
	}
	current := matches[0][1]
	if current == "" {
		current = matches[0][2]
	}
	if current != "./"+artifactName {
		t.Fatalf("request-form/%s runtime URL = %q, want %q", name, current, "./"+artifactName)
	}
	artifactTag := []byte(`  <script defer src="./` + artifactName + `" data-kit-drive="stable"></script>`)
	if bytes.Count(source, artifactTag) != 1 {
		t.Fatalf("request-form/%s exact artifact tag count != 1", name)
	}
	return bytes.Replace(source, artifactTag, []byte(scriptTags), 1)
}

func requestFormDriveScriptTags(artifactName, fetchSpyIntegrity, contractIntegrity string) string {
	return fmt.Sprintf(`  <script defer src="./request-form-fetch-spy.js" integrity="%s" crossorigin="anonymous"></script>
  <script defer src="./%s" data-kit-drive="stable"></script>
  <script defer src="./request-form-contract.js" integrity="%s" crossorigin="anonymous"></script>`,
		fetchSpyIntegrity, artifactName, contractIntegrity)
}

func requestFormDirectScriptTags(artifactName, contractIntegrity string) string {
	return fmt.Sprintf(`  <script defer src="./%s" data-kit-drive="stable"></script>
  <script defer src="./request-form-direct-contract.js" integrity="%s" crossorigin="anonymous"></script>`,
		artifactName, contractIntegrity)
}

const requestFormFetchSpySource = `
    globalThis.__requestFormFetches = [];
    globalThis.__requestFormNavigationAdds = 0;
    globalThis.__requestFormHeldSuccess = false;
    var __requestFormFetch = globalThis.fetch.bind(globalThis);
    globalThis.fetch = function (input, init) {
      var url = new URL(typeof input === "string" ? input : input.url, location.href);
      globalThis.__requestFormFetches.push({ url: url.href, init: init || null });
      var pending = __requestFormFetch(input, init);
      if (!globalThis.__requestFormHeldSuccess && url.pathname === "/api/profile" && url.search === "") {
        globalThis.__requestFormHeldSuccess = true;
        return pending.then(function (response) {
          return new Promise(function (resolve) {
            setTimeout(function () { resolve(response); }, 64);
          });
        });
      }
      return pending;
    };
    var __requestFormAdd = document.addEventListener.bind(document);
    document.addEventListener = function (type, listener, options) {
      if (type === "kit:navigation") globalThis.__requestFormNavigationAdds++;
      return __requestFormAdd(type, listener, options);
    };
`

const requestFormBrowserContractSource = browserHarness + "\n" + requestFormAssertions
const requestFormDirectBrowserContractSource = browserHarness + "\n" + requestFormDirectDesignAssertions

type requestFormSubmission struct {
	Name        string
	Email       string
	Demo        string
	Method      string
	ContentType string
	CSRF        string
}

type requestFormAPIState struct {
	mu          sync.Mutex
	submissions []requestFormSubmission
}

func (state *requestFormAPIState) add(value requestFormSubmission) {
	state.mu.Lock()
	state.submissions = append(state.submissions, value)
	state.mu.Unlock()
}

func (state *requestFormAPIState) snapshot() []requestFormSubmission {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]requestFormSubmission(nil), state.submissions...)
}

func handleRequestFormAPI(response http.ResponseWriter, request *http.Request, state *requestFormAPIState) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(response, "read body", http.StatusBadRequest)
		return
	}
	var payload struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(response, "invalid JSON", http.StatusBadRequest)
		return
	}
	demo := request.URL.Query().Get("demo")
	state.add(requestFormSubmission{
		Name:        payload.Name,
		Email:       payload.Email,
		Demo:        demo,
		Method:      request.Method,
		ContentType: request.Header.Get("Content-Type"),
		CSRF:        request.Header.Get("X-CSRF-Token"),
	})

	switch {
	case demo == "error":
		writeRequestJSON(response, http.StatusUnprocessableEntity, map[string]any{"message": "profile rejected"})
	case demo == "slow":
		writePendingRequestFormJSON(response, request, "slow request")
	case demo == "fast":
		writeRequestJSON(response, http.StatusOK, map[string]any{"message": "latest profile saved"})
	case payload.Email == "cancel@example.test":
		writePendingRequestFormJSON(response, request, "cancel request")
	case payload.Email == "cleanup@example.test":
		writePendingRequestFormJSON(response, request, "cleanup request")
	default:
		writeRequestJSON(response, http.StatusOK, map[string]any{"message": "profile saved"})
	}
}

func writePendingRequestFormJSON(response http.ResponseWriter, request *http.Request, message string) {
	body, _ := json.Marshal(map[string]any{"message": message})
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Content-Encoding", "identity")
	response.Header().Set("Content-Length", strconv.Itoa(len(body)))
	response.WriteHeader(http.StatusOK)
	first := len(body) / 2
	_, _ = response.Write(body[:first])
	if flusher, ok := response.(http.Flusher); ok {
		flusher.Flush()
	}
	select {
	case <-request.Context().Done():
		return
	case <-time.After(2 * time.Second):
		_, _ = response.Write(body[first:])
	}
}

func writeRequestFormHTML(response http.ResponseWriter, source []byte) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Encoding", "identity")
	response.Header().Set("Content-Length", strconv.Itoa(len(source)))
	_, _ = response.Write(source)
}

func assertRequestFormSubmissions(t *testing.T, submissions []requestFormSubmission) {
	t.Helper()
	var normal, failure, fast, cancel, cleanup bool
	for _, submission := range submissions {
		if submission.Method != http.MethodPost {
			t.Fatalf("request form API method = %q, want POST", submission.Method)
		}
		if !strings.HasPrefix(strings.ToLower(submission.ContentType), "application/json") {
			t.Fatalf("request form Content-Type = %q", submission.ContentType)
		}
		if submission.CSRF != "request-form-demo-token" {
			t.Fatalf("request form CSRF token = %q", submission.CSRF)
		}
		switch {
		case submission.Name == "Grace Hopper" && submission.Email == "grace@example.test" && submission.Demo == "":
			normal = true
		case submission.Demo == "error":
			failure = true
		case submission.Demo == "fast":
			fast = true
		case submission.Email == "cancel@example.test" && submission.Demo == "":
			cancel = true
		case submission.Email == "cleanup@example.test" && submission.Demo == "":
			cleanup = true
		}
	}
	if !normal || !failure || !fast || !cancel || !cleanup {
		t.Fatalf("request form API coverage normal=%t error=%t fast=%t cancel=%t cleanup=%t; submissions=%#v",
			normal, failure, fast, cancel, cleanup, submissions)
	}
}

const requestFormDirectDesignAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  await waitFor(function () {
    var host = document.querySelector('[data-kit-retain="request-progress"]');
    return globalThis.kit && host && host.hidden === true;
  }, "direct design route did not boot its idle retained progress component");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,progress,request",
    "direct design public service surface drifted");
  var host = document.querySelector('[data-kit-retain="request-progress"]');
  var bar = host && host.querySelector('[role="progressbar"]');
  var style = host && getComputedStyle(host);
  assert(host && bar && host.hidden === true && !bar.hasAttribute("aria-valuenow"),
    "direct design progress did not remain idle");
  assert(style.position === "fixed" && style.top === "0px" && style.pointerEvents === "none" && style.zIndex === "50",
    "direct design progress host is not fixed at the viewport top");
  assert(document.querySelectorAll("script[src]").length === 2,
	"direct design route did not use one sealed runtime plus one external contract");
  assert(!document.getElementById("request-profile-form"), "direct design route mounted the form component");
});`

const requestFormAssertions = `__runStandaloneKitTest(async function () {
  var assert = __kitTestAssert;
  var waitFor = __kitTestWaitFor;
  var root = document.documentElement;
  var form = document.getElementById("request-profile-form");
  var name = document.getElementById("request-profile-name");
  var email = document.getElementById("request-profile-email");
  var save = document.getElementById("request-profile-save");
  var latest = document.getElementById("request-profile-latest");
  var fail = document.getElementById("request-profile-fail");
  var cancel = document.getElementById("request-profile-cancel");
  var phase = document.getElementById("request-profile-phase");
  var message = document.getElementById("request-profile-message");
  var status = document.getElementById("request-profile-status");

  function anchor(element) {
    var rect = element.getBoundingClientRect();
    return { top: rect.top, left: rect.left, width: rect.width };
  }
  function sameAnchor(left, right) {
    return Math.abs(left.top - right.top) <= 0.5 &&
      Math.abs(left.left - right.left) <= 0.5 &&
      Math.abs(left.width - right.width) <= 0.5;
  }

  await waitFor(function () {
    return globalThis.kit && form && phase.textContent.trim() === "idle";
  }, "request form component did not boot");
  var progressHost = document.querySelector('[data-kit-retain="request-progress"]');
  var progressBar = progressHost && progressHost.querySelector('[role="progressbar"]');
  var progressStyle = progressHost && getComputedStyle(progressHost);
  assert(progressHost && progressBar && progressHost.hidden === true && !progressBar.hasAttribute("aria-valuenow"),
    "request progress did not boot idle");
  assert(progressStyle.position === "fixed" && progressStyle.top === "0px" &&
    progressStyle.pointerEvents === "none" && progressStyle.zIndex === "50",
    "request progress host is not fixed at the viewport top");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,progress,request",
    "request form public service surface drifted");
  assert(Object.isFrozen(globalThis.kit) && Object.isFrozen(globalThis.kit.request) &&
    globalThis.kit.service === undefined && globalThis.kit.bridge === undefined,
    "request form exposed mutable or private runtime controls");
  assert(globalThis.__requestFormNavigationAdds === 1, "progress service initialized more than once");
  assert(document.querySelectorAll("script[src]").length === 3,
	"request form did not use one prelude, one sealed runtime, and one external contract");

  function model(input, value) {
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }
  function recordsSince(index) {
    return globalThis.__requestFormFetches.slice(index);
  }
  function recordFor(records, query) {
    for (var index = 0; index < records.length; index++) {
      if (new URL(records[index].url).search === query) return records[index];
    }
    return null;
  }

  model(name, "Grace Hopper");
  model(email, "grace@example.test");
  var main = document.querySelector("main");
  var idleMainAnchor = anchor(main);
  var idleFormAnchor = anchor(form);
  save.click();
  await waitFor(function () {
    return phase.textContent.trim() === "pending" && progressHost.hidden === false;
  }, "successful save progress was not observable");
  assert(document.querySelector('[data-kit-retain="request-progress"]') === progressHost,
    "successful save replaced the progress host while active");
  assert(Math.abs(progressHost.getBoundingClientRect().top) <= 0.5 &&
    Math.abs(progressHost.getBoundingClientRect().height - 4) <= 0.5,
    "active progress host is not a four-pixel bar at the viewport top");
  assert(!progressBar.hasAttribute("aria-valuenow") &&
    progressBar.getAttribute("aria-valuetext") === "Loading",
    "successful save did not expose indeterminate progress while pending");
  assert(sameAnchor(anchor(main), idleMainAnchor) && sameAnchor(anchor(form), idleFormAnchor),
    "showing successful save progress moved the page or form");
  await waitFor(function () {
    return phase.textContent.trim() === "success" && status.textContent.trim() === "200";
  }, "modeled profile did not save through request@1.0.0");
  assert(message.textContent.trim() === "Saved Grace Hopper.", "component did not submit modeled name state");
  assert(progressHost.hidden === false && progressBar.getAttribute("aria-valuenow") === "100",
    "successful save did not expose its completed progress value");
  assert(sameAnchor(anchor(main), idleMainAnchor) && sameAnchor(anchor(form), idleFormAnchor),
    "completed successful save progress moved the page or form");
  await waitFor(function () {
    return progressHost.hidden === true && !progressBar.hasAttribute("aria-valuenow");
  }, "successful save progress did not return to idle");
  assert(document.querySelector('[data-kit-retain="request-progress"]') === progressHost,
    "hiding successful save progress replaced its host");
  assert(sameAnchor(anchor(main), idleMainAnchor) && sameAnchor(anchor(form), idleFormAnchor),
    "hiding successful save progress moved the page or form");

  fail.click();
  await waitFor(function () {
    return phase.textContent.trim() === "error" && status.textContent.trim() === "422";
  }, "HTTP failure did not reach component state");
  assert(message.textContent.trim() === "The server rejected this profile.", "HTTP error code was not rendered");

  var latestAt = globalThis.__requestFormFetches.length;
  latest.click();
  await waitFor(function () { return phase.textContent.trim() === "success"; }, "latest profile request did not settle");
  var latestRecords = recordsSince(latestAt);
  var slowRecord = recordFor(latestRecords, "?demo=slow");
  var fastRecord = recordFor(latestRecords, "?demo=fast");
  assert(slowRecord && fastRecord && slowRecord.init.signal.aborted === true && fastRecord.init.signal.aborted === false,
    "slow-to-fast keyed request did not keep the latest fetch");
  assert(message.textContent.trim() === "Saved Grace Hopper.", "superseded request changed latest component state");

  model(email, "cancel@example.test");
  var cancelAt = globalThis.__requestFormFetches.length;
  save.click();
  await waitFor(function () {
    return phase.textContent.trim() === "pending" && !cancel.disabled &&
      globalThis.__requestFormFetches.length === cancelAt + 1 &&
      progressHost.hidden === false && progressBar.hasAttribute("aria-valuenow");
  }, "cancel fixture did not become an active streamed request");
  var cancelRecord = globalThis.__requestFormFetches[cancelAt];
  assert(cancelRecord.init.signal.aborted === false, "cancel fixture was already aborted");
  cancel.click();
  await waitFor(function () {
    return phase.textContent.trim() === "cancelled" && cancelRecord.init.signal.aborted === true;
  }, "cancel button did not abort the active keyed request");

  model(email, "cleanup@example.test");
  var cleanupAt = globalThis.__requestFormFetches.length;
  save.click();
  await waitFor(function () {
    return phase.textContent.trim() === "pending" &&
      globalThis.__requestFormFetches.length === cleanupAt + 1 &&
      progressHost.hidden === false && progressBar.hasAttribute("aria-valuenow");
  }, "cleanup fixture did not become an active streamed request");
  var cleanupRecord = globalThis.__requestFormFetches[cleanupAt];
  assert(cleanupRecord.init.signal.aborted === false, "cleanup request was already aborted");

  document.getElementById("request-form-design").click();
  await waitFor(function () {
    return location.pathname === "/examples/request-form/design.html" &&
      !document.getElementById("request-profile-form") && cleanupRecord.init.signal.aborted === true;
  }, "Drive morph did not dispose the form and abort its active request");
  assert(document.documentElement === root, "request form navigation replaced the document root");
  assert(document.querySelector('[data-kit-retain="request-progress"]') === progressHost,
    "request form navigation replaced its retained progress host");
  assert(document.querySelectorAll("script[src]").length === 3,
	"incoming request form route changed its shared executable script topology");
  assert(Object.keys(globalThis.kit).join(",") === "version,component,progress,request",
    "request form navigation changed the public service surface");
  assert(globalThis.__requestFormFetches.length === cleanupAt + 2,
    "request form flow made an unauthored fetch; count was " + globalThis.__requestFormFetches.length);
});`
