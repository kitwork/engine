# Native HTTPS and FileRef upload

`app@1.15.0` selects `network@1.1.0`, `files@1.4.0`, `camera@1.1.0` and
`media@1.1.0`. Other service pins and app loader behavior are inherited from
`app@1.14.0`; old package source bytes remain immutable. These services are
available to trusted component JavaScript through `kit`; authored HTML actions
and `$app` expressions gain no network or upload authority.

```js
var controller = new AbortController();
var response = await kit.network.request({
  url: "https://api.example.com/v1/notes",
  method: "POST",
  headers: { Authorization: "Bearer ...", "Content-Type": "application/json" },
  body: JSON.stringify({ title: "Hello" }),
  timeoutMS: 15000,
  signal: controller.signal
});
// response: frozen { status, headers: frozen lowercase map, body: UTF-8 string }
// Non-2xx statuses are responses, so the app checks response.status.

var photo = await kit.camera.capture();
if (photo) {
  try {
    await kit.files.upload(photo, {
      url: "https://api.example.com/v1/photo",
      method: "POST",
      signal: controller.signal
    });
  } finally {
    await kit.files.release(photo);
  }
}
```

The trusted native manifest must grant the capability (`network.request` or
`network.upload`) and the exact HTTPS origin. Browsers without this native host
return `UNAVAILABLE`; these methods never fall back to browser fetch or bypass
the host policy. `network.online`, `snapshot`, `subscribe` and `status` retain
their earlier behavior.

Text bodies and responses are at most 1 MiB of valid UTF-8. GET is the default;
GET/HEAD have no body. Text requests also allow POST/PUT/PATCH/DELETE. Upload
defaults to POST and permits POST/PUT/PATCH only. Headers are explicit, with at
most 64 names and 16 KiB total; browser/cookie/host/proxy/connection headers are
denied. The timeout is 15 seconds by default and at most 30 seconds. Four
requests may run concurrently. Native DNS/IP/TLS policy and all authoritative
limits are implemented by `native/mobilehttp`.

Upload accepts only a live FileRef owned by this exact files service. The file
handle is read from its private WeakMap, passed to the private upload adapter,
and never exposed as a public FileRef property. The adapter is consumed and
removed while the exact graph installs. Blob content is not copied through the
JSON bridge; the native owner opens and streams the opaque file. This is a raw
file body upload, not multipart/form-data or durable background transfer.

`AbortSignal` cancellation settles promptly and requests native cancellation.
Every completed/failed/cancelled/timed-out operation receives best-effort
release; a begin response arriving after cancellation is released too. Polling
is bounded to 35 seconds, with a native expiry fence as final cleanup. A release
call failure never delays the public response.

`KitNetworkError` exposes a stable code and fixed message, never a raw URL,
header, body, or platform error. This service supplies transport; the app still
owns login, token refresh, request semantics, retries, and synchronization.

Verification includes native TLS/security/lifecycle tests, a Node contract for
options, cancellation and opaque FileRefs, and a real staged browser contract
using a mock native host. No external endpoint is required by these tests.
