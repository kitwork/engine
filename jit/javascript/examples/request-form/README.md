# Request form demo

This is the first small application slice built from sealed KitJS services. The
HTML knows only component state and methods. `request-form@1.0.0` privately calls
the lexical `kit.request` capability with plain JSON:

```js
await kit.request.post("/api/profile", {
  name: this.name,
  email: this.email
}, {
  key: "profile-save",
  timeout: 10000
});
```

## Run the successful API demo

Opening the HTML with `file://` or a static-only server cannot handle
`POST /api/profile`. From `engine/`, run the included same-origin Go server:

```powershell
go run ./jit/javascript/examples/request-form/server
```

Then open:

```text
http://127.0.0.1:4174/request-form/index.html
```

Or verify the successful endpoint directly from another PowerShell window:

```powershell
$requestHeaders = @{ "X-CSRF-Token" = "request-form-demo-token" }
$requestBody = @{ name = "Ada Lovelace"; email = "ada@example.test" } | ConvertTo-Json -Compress
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:4174/api/profile -ContentType application/json -Headers $requestHeaders -Body $requestBody
```

The response is:

```json
{"saved":true,"profile":{"name":"Ada Lovelace","email":"ada@example.test"}}
```

Click **Save profile**. The default request sends JSON plus the page's CSRF
token and receives `HTTP 200`; the component changes to `success` and displays
`Saved Ada Lovelace.`. The server is a bounded, standard-library demo endpoint,
not part of the KitJS runtime or sealed client artifact.

The demo exercises four real outcomes:

- normal submit resolves with the parsed server response;
- the error button receives a stable `HTTP` error code;
- latest-wins starts a slow and a fast request under the same key, so only the
  fast result may update the component;
- cancel stops the active keyed request explicitly.

The component also returns one synchronous cleanup from `init()`. Removing its
boundary aborts the same stable request key, without adding a document listener
or retaining an `AbortController` in presentation state.

`request@1.0.0` reports its lifecycle through `progress@1.0.0`.
`progress-bar@2.0.0` subscribes to progress and owns only its display state. Both
pages carry the same `data-kit-retain="request-progress"` host, so Hydrate moves
the exact progress component and subscription across navigation. Its four-pixel
bar is fixed to the top of the viewport, so showing or hiding network progress
does not move the form. Opening either page directly remains valid and requires
no app marker.

Both pages point to the same immutable artifact:

```text
hydrate.kit.0.9.0-next.12.f7b86e4c317e18003127ebb77e3f86e218a96d4133439cbaf696d93260be710c.js
```

From `engine/`, seal the final graph with:

```powershell
go run ./jit/javascript/cmd/assemble -profile hydrate -service progress=1.0.0=jit/javascript/service/progress/1.0.0.js -service request=1.0.0=jit/javascript/service/request/1.0.0.js -service-require request=progress=1.0.0 -component progress-bar=2.0.0 -component request-form=1.0.0 -component-require progress-bar=progress=1.0.0 -component-require request-form=request=1.0.0 -script progress-bar=jit/javascript/component/progress-bar/2.0.0.js -script request-form=jit/javascript/examples/request-form/request-form.js -canonical-dir jit/javascript/examples/request-form
```

The exact closed graph is:

```text
request-form@1.0.0 -> request@1.0.0 -> progress@1.0.0
progress-bar@2.0.0 -----------------> progress@1.0.0
```

The checked
`../kitjs.examples.cb95d3a46e8563f61a36a45167e067f1a1c3e74dbfc504a175358b23802dc881.css`
is generated only from literal Tailwind utilities across the current service
demos; it contains no hand-written rule or runtime class generator. From
`engine/`, reproduce it with:

```powershell
go run ./jit/javascript/cmd/styles -canonical-dir jit/javascript/examples jit/javascript/examples/preferences/index.html jit/javascript/examples/drive-progress/index.html jit/javascript/examples/drive-progress/next.html jit/javascript/examples/request-form/index.html jit/javascript/examples/request-form/design.html
```

The `demo=slow`, `demo=fast`, and `demo=error` query values are implemented by
the Go browser-test server. They make replacement and failure deterministic.
