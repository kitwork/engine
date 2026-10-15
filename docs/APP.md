# Kitwork App Manifest

`app.kitwork.js` is the executable manifest for one Kitwork host. It declares:

- which application layout the host owns;
- which surfaces the process exposes;
- which databases and external connections are available;
- which work must finish before the host reports ready;
- and, for a website, which port starts the HTTP server.

The manifest is evaluated once during boot by Kitwork's constrained JavaScript
runtime. It is configuration expressed in Kitwork syntax, not tenant request
code. Declaring a surface records intent; listeners and runtime owners are
created only after the complete manifest has compiled and validated.

```javascript
import { app, env } from "kitwork";

app
  .website({
    hostname: env.APP_HOST || "example.com",
    allowLocal: env.ALLOW_LOCAL || false
  })
  .run(env.PORT || 8080);
```

New applications should use `app.kitwork.js` and the `app` binding.
`server.kitwork.js`, `server`, and the older flat builder methods remain
compatibility paths.

## Mental model

The API has four different kinds of declaration. Keeping them separate makes a
manifest easier to read and prevents credentials or listener behavior from
ending up in the wrong layer.

| Kind | Meaning | Examples |
| --- | --- | --- |
| Host configuration | Process-wide behavior | `root`, `logger` |
| Surface | Something the app offers | `website`, `desktop`, `mobile`, `database`, `gateway`, `proxy` |
| Connection | An external resource the app consumes | `postgresql`, `mysql`, `sqlite`, `redis`, `connect` |
| Lifecycle | Preparation and startup | `prewarm`, `run` |

Most methods return `app`, so declarations compose as one chain. `app.run()` is
different: it is the terminal operation for the website server and must be the
last call in that chain.

## Start here

### One local website

```javascript
import { app, env } from "kitwork";

app
  .root("app")
  .website({
    hostname: "notes.localhost",
    allowLocal: true,
    hotReload: true
  })
  .run(env.PORT || 8080);
```

Open `http://notes.localhost:8080`. This example uses no TLS, watches source
files, and runs one application from `app/`.

### One production website

```javascript
import { app, env } from "kitwork";

app
  .root("app")
  .logger({
    level: env.LOG_LEVEL || "info",
    format: "json",
    logfile: "./logs/app.log",
    errorfile: "./logs/error.log",
    max_size: 20,
    max_backups: 5,
    max_age: 30,
    compress: true
  })
  .website({
    hostname: "example.com",
    canonical: "apex",
    domains: ["example.com", "www.example.com"],
    bytecodeCache: true,
    rateLimit: {
      rate: 2000,
      ip: 120,
      browser: 240,
      user: 120,
      period: "1s"
    }
  })
  .run(env.PORT || 8080);
```

When `allowLocal` is false, production TLS and AutoSSL behavior remain active.
The declared hostname is prepared before readiness unless an explicit
`prewarm(...)` policy replaces that default.

## Application roots

The root determines runtime ownership, filesystem routing, scheduler scope,
environment isolation, and cache boundaries. It is not only a directory name.

### Single application, single site

```text
app/
  router.kitwork.js
  page.kitwork.html
  api/
    router.kitwork.js
```

```javascript
app.root("app").website("example.com").run(8080);
```

The root itself is the site. Requests use the configured website hostname.

### Single application, multiple domains

```text
app/
  example.com/
    router.kitwork.js
  docs.example.com/
    router.kitwork.js
```

```javascript
app
  .root("app")
  .website({
    hostname: "example.com",
    domains: ["example.com", "docs.example.com"]
  })
  .prewarm("example.com", "docs.example.com")
  .run(8080);
```

Both domains share one App Runtime and its app-owned `_cron/`, `_queue/`,
`_core/`, and `.data/` resources while retaining separate Site and Generation
runtimes.

### Multiple tenant applications

```text
apps/
  <identity>/
    <domain>/
      router.kitwork.js
```

```javascript
app
  .root("apps")
  .website({
    hostname: "kitwork.io",
    allowLocal: env.ALLOW_LOCAL || false
  })
  .run(env.PORT || 8080);
```

Each identity owns an isolated App Runtime. Each domain below that identity is
a site inside the app boundary.

### Automatic root discovery

Omit `app.root(...)` when exactly one of `app/` or `apps/` exists. Kitwork
inspects both names and selects the one present. If both exist, startup fails
with an ambiguity error instead of silently choosing one.

Use an explicit root in production when deployment layout is intentional:

```javascript
app.root(env.ROOT || "apps");
```

## Host environment

The manifest receives the root `.env` file plus the real process environment.
Operating-system values win when the same key exists in both sources.

```javascript
import { app, env } from "kitwork";

app.website({
  hostname: env.APP_HOST || "example.com",
  allowLocal: env.ALLOW_LOCAL || false
}).run(env.PORT || 8080);
```

Values are coerced when possible:

```text
"8080"  -> number 8080
"true"  -> boolean true
"false" -> boolean false
other    -> string
```

Require a secret instead of providing an unsafe fallback:

```javascript
password: env.require("DATABASE_PASSWORD")
```

Do not use common operating-system keys such as `HOSTNAME`, `HOST`, `USER`,
`PATH`, `HOME`, or `PWD` for app configuration. Prefer namespaced keys such as
`APP_HOST`, `APP_PORT`, and `APP_DATABASE_PASSWORD`.

Host environment and tenant environment are isolated. A tenant does not gain
access to secrets merely because the manifest used them to configure a host
resource.

## Website

`app.website()` declares filesystem-routed websites and APIs. The port belongs
to `app.run()`, giving server startup one unambiguous location.

### Short form

```javascript
app.website("example.com").run(8080);
```

### Full form

```javascript
app
  .website({
    hostname: "example.com",
    allowLocal: false,
    trustProxy: false,
    hotReload: false,
    canonical: "apex",
    redirects: {
      "old.example.com": "example.com",
      "docs-old.example.com": "https://docs.example.com"
    },
    domains: ["example.com", "www.example.com", "docs.example.com"],
    bytecodeCache: true,
    bytecodeCacheDir: "./.cache/bytecode",
    rateLimit: {
      rate: 2000,
      ip: 120,
      browser: 240,
      user: 120,
      period: "1s"
    }
  })
  .run(env.PORT || 8080);
```

### Website options

| Option | Purpose |
| --- | --- |
| `hostname` | Primary hostname and local-domain fallback. It is a hostname, not a URL. |
| `allowLocal` | Disable production TLS/ACME behavior for local development. |
| `trustProxy` | Trust forwarded client identity headers from an edge you control. |
| `hotReload` | Watch application files and replace prepared generations during development. |
| `canonical` | `"apex"`, `"www"`, or an empty value to disable canonical redirects. |
| `redirects` | Map incoming hosts to another host or absolute URL while retaining path/query. |
| `domains` | Additional hostnames used by routing and certificate policy. |
| `bytecodeCache` | Enable the prepared bytecode cache. |
| `bytecodeCacheDir` | Select a persistent bytecode-cache directory and enable the cache. |
| `rateLimit` | Apply bounded host-level request gates before tenant resolution. |

`website()` deliberately rejects `port`. Use:

```javascript
app.website("example.com").run(8080);
```

Do not use:

```javascript
app.website({ hostname: "example.com", port: 8080 });
```

### Local hostname mapping

With `allowLocal: true`, Kitwork supports production-shaped local addresses
without editing the hosts file:

```text
http://example.com.localhost:8080 -> example.com site
http://localhost:8080             -> configured primary hostname
```

An exact site directory already ending in `.localhost` remains exact and is
not rewritten.

### Running behind a trusted edge

```javascript
app.website({
  hostname: "example.com",
  trustProxy: true
}).run(8080);
```

Enable `trustProxy` only when an operator-controlled proxy removes and rewrites
forwarded headers. Otherwise a client can spoof identity used by rate limits,
scheme detection, redirects, and request metadata.

### Host rate limiting

Status: proposed public contract. The current engine accepts one host-level
rule through `app.rateLimit(...)` or `website({ rateLimit: ... })`. It does not
yet implement the canonical lowercase API or stacked host windows described in
this section.

The host rate limiter runs before tenant resolution, VM acquisition, route
matching, rendering, or gateway dispatch. It is therefore an app policy rather
than a presentation detail of one website. The same host boundary may receive a
website request or dispatch an operator-controlled gateway target.

Calling `ratelimit` only declares manifest data. It does not create a timer,
worker, or listener during manifest evaluation.

#### Three enforcement levels

Kitwork exposes rate limits at three ownership boundaries:

| API | Owner | Scope | Evaluated |
| --- | --- | --- | --- |
| `app.ratelimit(...)` | Host runtime | Website and HTTP gateway ingress | Before tenant resolution |
| `router.ratelimit(...)` | Site runtime | A folder and every descendant | During route resolution |
| `method.limit(...)` | Route method | One HTTP endpoint | Before its handler |

Use the highest boundary that expresses the policy. Host limits protect the
process, folder limits protect a section of one site, and method limits protect
an operation such as sign-in, search, upload, or checkout.

Host limits do not implicitly apply to `app.proxy()` forward-proxy listeners.
A forward proxy owns different protocols, identities, and abuse controls.

#### Canonical API

One call declares one time window. Calls append instead of replacing earlier
windows:

```javascript
app
  .website("example.com")
  .ratelimit({ ip: 30, second: 1 })
  .ratelimit({ ip: 600, minute: 1 })
  .ratelimit({ ip: 10000, hour: 1 })
  .run(8080);
```

Every request must pass every configured window. A failure in any dimension of
any window rejects the request with HTTP `429 Too Many Requests`.

#### Dimensions

```javascript
app.ratelimit({
  rate: 5000,
  ip: 1000,
  browser: 300,
  user: 600,
  minute: 1
});
```

| Field | Meaning |
| --- | --- |
| `rate` | Aggregate requests across the complete host boundary. |
| `ip` | Requests attributed to one resolved client IP address. |
| `browser` | Requests attributed to one browser identity when available. |
| `user` | Requests attributed to one authenticated user identity when available. |

At least one positive dimension is required. A missing dimension is disabled
for that window. Zero and negative configured values are invalid instead of
silently disabling the field.

`rate` remains the aggregate field for compatibility with the existing host
configuration. Introducing `global` as a second spelling would make storage,
metrics, and migration needlessly ambiguous.

#### Time windows

Use one readable unit field:

```javascript
app.ratelimit({ ip: 30, second: 1 });
app.ratelimit({ ip: 600, minute: 1 });
app.ratelimit({ ip: 10000, hour: 1 });
app.ratelimit({ ip: 100000, day: 1 });
```

Use `period` for a custom duration:

```javascript
app.ratelimit({ ip: 300, period: "5m" });
app.ratelimit({ ip: 20, period: "500ms" });
```

Exactly one of `second`, `minute`, `hour`, `day`, or `period` is allowed. Unit
values must be positive integers. `period` must be a positive Go duration
string. A missing window defaults to `second: 1` only for compatibility with
the old host rule; new manifests should always declare the window.

#### Stacking semantics

Stacking represents burst and sustained policies without combining unrelated
windows into one object:

```javascript
app
  .ratelimit({ rate: 1000, ip: 30, second: 1 })
  .ratelimit({ rate: 30000, ip: 600, minute: 1 });
```

The first window absorbs short bursts. The second prevents a client that stays
just below the per-second ceiling from consuming the host indefinitely.

Declaration order must not change the observable policy. The runtime may check
cheaper or shorter windows first, but a request accepted by all rules consumes
one unit from every applicable bucket. If a later check rejects the request,
earlier provisional consumption must be rolled back so rejected traffic cannot
silently exhaust unrelated windows.

An exact duplicate window is a configuration error. Different windows may use
the same identity dimension.

#### Identity and trusted proxies

IP limits use the direct peer address unless the operator explicitly enables
trusted proxy handling:

```javascript
app
  .website("example.com")
  .trustProxy(true)
  .ratelimit({ ip: 600, minute: 1 })
  .run(8080);
```

Enable `trustProxy(true)` only behind an edge that removes client-supplied
forwarding headers and writes its own. Otherwise a client can rotate a forged
`X-Forwarded-For` value to evade the IP budget.

Browser and user dimensions apply only when the host has a trustworthy identity
for that request. Missing optional identity must not collapse all anonymous
traffic into one shared empty key; aggregate and IP dimensions still apply.

#### Rejection response

A rejected request returns a small host-owned response without acquiring a VM
or rendering a template:

```text
HTTP/1.1 429 Too Many Requests
Retry-After: <whole seconds>
```

`Retry-After` describes the earliest failed window that can accept another
request. Public responses must not reveal raw user, browser, or IP bucket keys.

#### Runtime ownership

The host runtime owns app-level limiter state because it must survive site
generation replacement and operate before a site is selected. Reloading one
tenant must not reset a host budget.

Site and route budgets remain owned by the site runtime. They survive generation
replacement according to the runtime stability contract but remain isolated by
site identity.

In a multi-process or multi-node deployment, app-level budgets are local to one
process until an explicit distributed limiter exists. Documentation and
diagnostics must not imply a cluster-wide guarantee.

#### Compatibility and migration

The implementation should introduce lowercase `app.ratelimit(...)` as the
canonical method and preserve these existing inputs:

```javascript
app.rateLimit({ ip: 120, period: "1m" });

app.website({
  hostname: "example.com",
  rateLimit: { ip: 120, period: "1m" }
});
```

Each compatibility form normalizes to one host window. A manifest must not mix
legacy and canonical declarations; doing so is an actionable configuration
error rather than an implicit merge.

Migration proceeds in this order:

1. Parse the old object into a one-item rule list.
2. Add lowercase builder support that appends normalized rules.
3. Keep `app.rateLimit` as a compatibility alias.
4. Remove `website({ rateLimit })` from new examples.
5. Publish new type signatures only after runtime tests pass.

#### Validation

Manifest evaluation rejects:

- `app.ratelimit()` without an object;
- a rule without a positive dimension;
- zero, negative, fractional, or non-numeric limits;
- more than one time-window field;
- a non-positive or invalid `period`;
- exact duplicate windows;
- mixed canonical and legacy host declarations;
- unsupported fields instead of silently ignoring misspellings.

Errors identify the call index and field:

```text
app.ratelimit rule 2: minute must be a positive integer
```

#### Implementation acceptance criteria

The API leaves proposed status only when:

- repeated calls append immutable normalized rules;
- second, minute, hour, day, and custom periods parse consistently;
- every configured dimension is enforced;
- partial bucket consumption rolls back on rejection;
- budgets survive tenant generation reload;
- host limits run before website or gateway dispatch;
- forward-proxy listeners remain unaffected;
- trusted and untrusted client-IP tests pass;
- `Retry-After` identifies the failed window;
- existing `app.rateLimit` and website object manifests keep defined behavior;
- `kitwork.d.ts`, this guide, public documentation, and examples match the
  executable contract.

### Prewarm

Sites are loaded on demand by default. A hostname declared through
`app.website(hostname).run(port)` is the implicit primary readiness target.
Use `prewarm` to replace that default policy:

```javascript
app.prewarm();
```

Prepare the configured primary hostname.

```javascript
app.prewarm("example.com", "docs.example.com");
```

Prepare an exact list of domains.

```javascript
app.prewarm("*");
```

Prepare every site discovered below the selected root.

```javascript
app.prewarm(false);
```

Keep every site request-driven and cold until first access.

`"*"` must be the only argument; it is a complete selector, not a glob.
Explicitly selected sites are readiness requirements. A preparation failure
stops startup instead of publishing a partially ready host.

Prewarming compiles routes, prepares templates and presentation assets, and
runs router initialization. It does not invoke request handlers and does not
pin a generation against ordinary idle eviction. Prefer exact domains unless
an all-sites deployment has a measured memory budget.

## Desktop

### Remote website preview on Windows

The Windows reference shell supports an experimental remote website mode:

```javascript
import { app } from "kitwork";

app.desktop({
  name: "Kitwork",
  url: "https://kitwork.io",
  width: 1280,
  height: 820,
  native: true,
  splash: "./splash.html",
  menu: false,
  terminal: false,
  development: false
});
```

From `native/`, build `cmd/kitwork-desktop`, then launch the executable with
`--config=examples/remote-desktop/app.kitwork.js`. This mode loads HTTPS directly
without a tenant runtime or native capability bridge. `native: true` (default)
uses the OS titlebar. `native: false` removes the titlebar and gives the website
the full client area; it does not inject HTML window buttons into a remote page.
Alt+F4 closes the app; Alt+Space opens the Windows system menu for moving,
resizing, restoring and minimizing. The remote page receives no window-control
bridge in either mode. Legacy `chrome: "html"` selects the same frameless mode.
Do not combine `url` with `domain`, `start`, or `permissions`.
Use explicit `--config`, or place `app.kitwork.js` beside the executable and
double-click it without arguments. URL string shorthand is not supported.
This is a preview, not a release packaging path:
Installer generation and signed release packaging remain future work.

`external` defaults to `true`: cross-origin HTTP(S) navigation opens in the
system browser, including redirects and new-window links. Same-origin popup
links navigate the current WebView. Origin comparison includes scheme, host,
and effective port. Unsupported schemes and URLs with credentials are blocked.
`external: false` keeps HTTP(S) navigation inside the WebView. This policy
handles top-level navigation and popups; it does not restrict subresources or
iframe requests.

`maximized: true` opens the remote window maximized without covering the taskbar;
the titlebar follows `native`. It defaults to `false`; width and height remain the
restored window dimensions.

`menu` controls the default WebView context menu and defaults to `true`.
`splash` is disabled by default in the Windows desktop shell (local or remote). Without an
explicit splash declaration the application navigates directly to the website,
even if a splash file exists beside the manifest; no loading delay is added.
When enabled with `splash: true` or `splash: 1.5`, the application requires
`splash.kitwork.html` beside the selected `app.kitwork.js`. A number is the minimum
visible duration in seconds (fractional values supported, range 0..60).
Use `splash: "./splash.html"` (or another relative `.html` path within that
directory) to require a specific file. Missing explicit files fail clearly;
Missing files fail clearly: no legacy filename or generated splash replaces them.
The file must be nonempty, regular, and at most 256 KiB. Editing it requires
restarting the application, not rebuilding the executable. `splash: false` skips it.

```js
app.desktop({ name: "Studio", splash: 1.5 });
// Equivalent: splash: { duration: 1.5 }
// Custom file and data:
app.desktop({
  name: "Studio",
  splash: { template: "./loading.kitwork.html", duration: 2, bind: { message: "Welcome" } }
});
```

Omitting `duration` adds no intentional wait. Duration starts when the splash is
shown, not when the process starts. `app.splash(1.5)` accepts the same shorthand.
Plain `.html` files are passed to the WebView exactly as authored: no placeholder
replacement, implicit theme changes, or injected CSS. Write a complete standalone
HTML document with its own styles. A `.kitwork.html` filename selects Kitwork's
normal template renderer instead; standard expressions such as `{{ title }}` and
`{{ theme }}` use the application title and the system theme (`dark` or empty).
Normal template blocks and escaped output work, and JIT CSS is generated from
the rendered utility classes and inserted into the head automatically. There is
no special title or CSS placeholder syntax. The included `.html` example is a
pre-rendered, self-contained document; the `.kitwork.html` example is its source.
Relative images, CSS and fonts now resolve against the splash file's directory,
including nested templates (for example `screens/splash.html` with `./logo.svg`).
The shell intercepts a private HTTPS `.invalid` origin in-process; it opens no
localhost port and exposes no native bridge. The manifest directory is the
filesystem boundary, enforced with `os.Root` (including symlink confinement).
Only CSS, SVG/PNG/JPEG/WebP/GIF/ICO/AVIF and WOFF/WOFF2/TTF/OTF files are served,
at most 8 MiB each and 32 MiB total per loading screen. Hidden paths, traversal, directories, scripts, manifests and
databases are refused. The asset root is closed when website navigation begins.
A response CSP permits local styles/images/fonts but blocks scripts, frames,
forms and external resources. An authored CSP may restrict this further: add
`'self'` to `img-src`, `style-src` or `font-src` when using local files.

```html
<img src="./assets/logo.svg" alt="Application logo" width="64" height="64">
```

### Native Window Colors

`desktop.background` sets the Windows native titlebar, host-window background,
and opaque WebView fallback before page paint. `desktop.color` sets native
titlebar text only. They do not inject CSS or recolor remote websites, HTML
titlebars, or authored templates. Windows still controls its caption buttons.
Custom native titlebar colors are best-effort DWM attributes on supported Windows
versions; unsupported versions keep their system titlebar. WebView fallback
colors are verified through the controller's color getter.
Omitted background follows the OS light/dark theme. Omitted color uses contrast
against the native background. Explicit `color: "system"` instead follows the OS
theme, even with a custom background; choose contrast or custom text if needed.

```javascript
app.desktop({
  name: "Kitwork",
  url: "https://kitwork.io",
  background: { light: "#ffffff", dark: "#09090b" },
  color: { light: "#171717", dark: "#fafafa" },
  launch: { width: 420, height: 240, rounded: 12, background: "#123456" }
});
```

Each color accepts `"system"`, an opaque `#RRGGBB` string or an object containing exactly
`light` and `dark` hex colors. CSS names, short hex and alpha colors are rejected.
`"default"` is not an alias. System mode uses a neutral Windows theme palette
(white/black in light mode, #202020/white in dark mode), not the user's accent
color. It follows Windows settings independently of the document's theme and
refreshes on system theme notifications. It does not modify HTML/CSS.
The remote preview selects system appearance at startup; local apps can update
native colors through their existing resolved-appearance lifecycle.
Launch inherits `desktop.background` unless `launch.background` overrides it.

```javascript
app.desktop({ name: "Studio", background: "system", color: "system" });
```
The default launch HTML has a transparent body so the configured fallback shows;
an authored HTML background takes precedence. Edit the adjacent manifest and
restart to apply changes; no binary rebuild is needed.

### Windows Window Position

```javascript
app.desktop({ name: "Studio", width: 800, height: 600, position: "top-left" });
app.desktop({ name: "Studio", position: { x: 24, y: 24 } });
// Remote launch has an independent placement:
app.desktop({ url: "https://kitwork.io", position: "center", launch: { position: "bottom-right" } });
```

`position` defaults to `"center"`. The other anchors are `"top-left"`,
`"top-right"`, `"bottom-left"`, and `"bottom-right"`. Coordinates are integer
logical pixels (0..100000), relative to the selected monitor's work-area origin,
not global desktop coordinates. DPI scales offsets; taskbar space is excluded;
off-screen offsets are clamped so the window stays visible. Negative monitor
origins are supported. Position is applied only at startup, not every resize.
`maximized: true` ignores main-window position. Do not combine `position` with
legacy `center`. Splash shares the main window's position. The existing launch
surface accepts its own position where supported by the remote Windows shell.

### Windows Window Materials

```javascript
app.desktop({
  name: "Kitwork",
  url: "https://kitwork.io",
  native: true,
  material: "mica", // "none" (default), "mica", or "glass"
  background: { light: "#ffffff", dark: "#202020" }
});
```

`material` selects a native Windows backdrop for the main window, independently
of `launch.glass`. `mica` uses the long-lived-window Mica material; `glass`
uses Desktop Acrylic. Windows 11 build 22621+ is required for this API.
Older systems retain the configured opaque WebView background. Windows controls
material appearance, accessibility and power-saving fallbacks. Mica is not a
live view through to applications behind the window.

The shell makes the WebView default background transparent, but never rewrites
the website CSS. An opaque HTML/body/container still covers the material; author
transparent backgrounds (for example `bg-transparent`) where it should show.
Custom `background` remains the fallback; native material owns the caption fill.
Both remote previews and local app folders consume this option on Windows.

Prefer one `desktop` object with shared `bind` data for all local templates:

```javascript
app.desktop({
  name: "Kitwork",
  url: "https://kitwork.io",
  bind: { title: "Kitwork", logo: "/assets/logo.svg" },
  launch: { width: 420, height: 240 },
  splash: true,
  offline: true
});

// Compatibility shorthand; do not also set desktop.splash.
app.desktop({ name: "Kitwork", url: "https://kitwork.io" })
  .splash("./splash.kitwork.html", {
    heading: "Welcome",
    message: "Getting things ready"
  });
```

The template uses ordinary expressions such as `{{ title }}`, `{{ logo }}` and
`{{ message }}`. Objects can omit `template` to use the default, or name a custom
file; object form always renders it, even with a plain `.html` filename.
`desktop.bind` defaults to an empty object and accepts JSON-compatible
data only (including nested arrays/objects), capped at depth 32 and 10,000 values.
Supplied bindings override the default `title`/`theme` keys without mutating the
manifest data. It feeds only local launch/splash/offline templates, never a remote
website or tenant VM. Keep secrets out of it. Defaults are merged first, shared
data second; legacy per-screen `bind` overrides remain supported for compatibility.
Offline's `retry`, `error`, and `status` always come last and are protected.
Relative URLs resolve against each template directory; use `/assets/logo.svg`
for shared assets when templates live in different subdirectories.
Both app-level call orders work, but mixing app-level and desktop
splash declarations or repeating a splash declaration is rejected. `app.splash`
records presentation data; it does not create a surface or server. The current
consumer is the Windows remote desktop preview, not mobile or website startup.

The main window remains hidden while its single WebView initializes and loads
the local HTML. The included example uses an SVG mark and system light/dark appearance. The main window
then appears displaying that document. After the configured minimum duration it navigates
to the configured HTTPS website in the SAME controller. There is no second
WebView, overlay teardown, popup, or window/controller recreation for splash itself.
An optional separate launch WebView is closed during handoff. Window size,
titlebar, and maximized state remain unchanged. The controller's fallback canvas
color matches the loading document to avoid a white canvas during navigation.
The local-document initialization has a 15-second fallback to start website
navigation. Set `splash: false` to skip the document and delay. The website starts
loading after the local loading screen, not concurrently behind it. Navigation
may still change the background if the website uses a different palette; this
is not a crossfade.

Remote Windows preview now shows an embedded connection-error document when
top-level navigation fails (network, DNS, or certificate verification), rather
than leaving a blank or browser error page. Its **Try again** link reloads the
configured desktop URL in the same WebView and window. No native bridge or
additional listener is involved. Certificate errors
never offer a bypass or disable TLS verification.

Each top-level navigation has a 30-second UI timer; redirects within the same
navigation keep that deadline. A new navigation replaces the previous attempt,
and stale completion events cannot overwrite its result. A timeout replaces the
pending document with the connection screen; closing the window cancels the
timer. Explicit cancellations do not display an error. This handles transport
and loading failures, not successful HTTP responses containing a site's own
404/500 page. It does not cache the remote website for offline use. The embedded
error UI uses the system theme captured at startup, generated Tailwind utilities,
and script-free CSP. Retry returns to `desktop.url`, not the failed deep link.

### HTML Launch Window

`launch` is a separate, opt-in window with its own WebView2, displaying
`launch.kitwork.html`. Its width and height are independent of the desktop window.
It cannot appear until its own WebView is initialized; no extra native loading
screen is shown before it.

```javascript
app.desktop({
  name: "Kitwork",
  url: "https://kitwork.io",
  width: 1280,
  height: 820,
  bind: { title: "Kitwork", logo: "/assets/logo.svg" },
  launch: {
    width: 420,
    height: 240,
    rounded: 12,
    template: "./launch.kitwork.html"
  },
  offline: true
});
```

`launch: true` uses the default 420 x 240 window and adjacent/default template.
`launch.glass: true` optionally enables Windows Desktop Acrylic (Windows 11
22H2 or later). It defaults to false. The launch HTML must use a transparent
body/background to reveal it. Unsupported systems retain `launch.background`.
This option affects only the launch window, not the website or desktop window.
Windows controls the tint, blur and transparency (including accessibility and
power-saving policies); `glass` does not force translucency when Windows disables it.
`launch: {}` does the same; object fields override width, height, rounded and template.
Omission or `launch: false` means no launch window, even if the file exists.
An omitted `template` discovers the adjacent `launch.kitwork.html` or uses the
embedded version. An explicit missing file is a startup error. Templates are
rendered with shared data and JIT CSS; images, fonts and CSS use the confined
private origin, without a network listener, scripts, native bridge or remote
navigation. Popups and navigation to other URLs are blocked.

Width is an integer from 240 to 2048; height is from 120 to 1200. These are logical
pixels, DPI-scaled and capped to 85% of the monitor work area. The centered,
frameless launch is an independent temporary app window, so it remains visible
while the desktop window is hidden. `rounded` is an integer radius from 0 to
128 logical pixels, defaulting to 12. Set `rounded: 0` for square corners.
The native window region clips both the host and WebView, including on Windows 10.
Its default template receives `title`, `theme` and a starting message, overridden
by shared data. Compatibility `title`, `message`, and per-screen `bind` fields
remain accepted, but new configurations should use `desktop.bind`.

The launch host stays hidden until its HTML loads (bounded by a 15-second local
document timer). The desktop stays hidden during its own initialization. Once ready, it is shown before
the launch window is destroyed: it displays the configured splash, or navigates
directly to the website if splash is omitted. Launch remains visible for at least
800 ms to avoid a one-frame flash on warm startup. Main WebView initialization
counts toward that interval; native messages continue to run during any remaining
wait. Without splash, launch ends when WebView is
initialized, not after the remote website finishes downloading. Closing launch
(Alt+F4) cancels application startup. Destroying it during a normal transition
does not close the desktop or post a quit message. The auxiliary controller and
asset root are closed at handoff; this adds a transient WebView and startup/memory
cost, not a permanent second browser. `app.launch(...)` remains a compatibility
shorthand; do not combine it with `desktop.launch`. This preview currently
supports the Windows remote desktop consumer only, not mobile or local tenants.

### Custom Offline Document

The Windows remote preview automatically reads `offline.kitwork.html` next to
the manifest. If absent, it uses the embedded default. Both the loading and
offline documents are ordinary files you can customize and distribute:

```text
Kitwork.exe
app.kitwork.js
launch.kitwork.html
splash.kitwork.html
offline.kitwork.html
assets/logo.svg
```

Declare a specific document through the desktop option. Compatibility shorthand
still works. As with splash, declarations can come before or after `desktop`, but
duplicates and mixing equivalent declarations are errors.

```javascript
app.desktop({
  name: "Kitwork",
  url: "https://kitwork.io",
  bind: { logo: "/assets/logo.svg" },
  offline: {
    template: "./offline.kitwork.html"
  }
});
```

Templates receive `title`, `theme`, `heading` and `message` defaults. Shared `desktop.bind`
can override these presentation fields. Three operational fields are reserved
and cannot be overridden by bindings:

- `retry`: private shell-owned retry address, usable as an anchor's `href`.
- `error`: `network`, `tls` or `timeout`.
- `status`: the numeric WebView2 error status (timeout uses `7`).

```html
<h1>{{ heading }}</h1>
<p>{{ message }}</p>
<a href="{{ retry }}">Try again</a>
```

Retry only works while the shell is displaying an offline document; a remote
website cannot invoke it. Clicking it cancels the private navigation and loads
the configured desktop URL. Scripts and native bridge messages are unnecessary.
If authoring raw `.html`, write
`<a href="https://retry.kitwork.invalid/">Try again</a>` instead: raw HTML remains
unchanged. String `.kitwork.html` paths and all `{ template, bind }` objects use
normal escaped template rendering and JIT CSS. Templates are read at startup,
then bound to each failure's status; restart after editing their source.

`offline: true` or `offline: {}` uses adjacent/default discovery. `offline: false` or
`app.offline(false)` disables the recovery document and its 30-second deadline,
leaving WebView2's normal failure behavior. Explicit missing files fail startup,
not silently fall back. HTML files and bindings use the same 256 KiB, depth-32,
10,000-value limits as splash. Relative local images, CSS and fonts use the same
confined private origin and 8 MiB/file, 32 MiB/document budget. The root closes
before remote navigation; repeated retries reuse one resource filter rather
than accumulating listeners. Keep `'self'` in an authored CSP for local assets.
This is a connection fallback, not a service worker or an offline website cache.
The current consumer remains Windows remote desktop preview only.

For distribution, keep the executable, `app.kitwork.js`, local templates and their
asset directory together. The executable discovers the adjacent manifest when
started without flags, regardless of the current working directory. A Windows
WebView2 Runtime installation remains required; this preview is not an installer.
`development` enables DevTools and defaults to `false` in this preview; it does
not yet enable hot reload or other development services. `terminal` is a Windows
build option and defaults to `false`. Run
`powershell -File examples/remote-desktop/build.ps1` from `native/` to read that
option and choose the executable subsystem. A direct `go build` does not read
the manifest's terminal setting. Changing terminal requires rebuilding.

`app.desktop()` records native application identity and window behavior in one
flat object. The cloud host records the declaration but does not open a window;
the desktop build or shell tool consumes it.

### Native operating-system window

```javascript
app.desktop({
  name: "Kitwork Studio",
  icon: "./assets/icon.png",
  width: 1280,
  height: 820,
  minWidth: 960,
  minHeight: 640,
  native: true,
  resizable: true,
  center: true,
  maximized: false
});
```

### HTML window chrome

```javascript
app.desktop({
  name: "Kitwork Studio",
  title: "Workspace",
  icon: "./assets/icon.png",
  width: 1280,
  height: 820,
  native: false,
  resizable: true,
  center: true
});
```

When `native` is false, the application owns its titlebar and drag surface in
HTML. `title` is optional and defaults to `name`. Dimensions are logical pixels
and scale with display DPI. `icon` is the source used for the executable,
launcher, taskbar, and window where supported by the target platform.

### One app for web and desktop

```javascript
import { app, env } from "kitwork";

app
  .root("app")
  .website({
    hostname: "studio.kitwork.localhost",
    allowLocal: true
  })
  .desktop({
    name: "Kitwork Studio",
    icon: "./assets/icon.png",
    width: 1280,
    height: 820,
    native: false,
    center: true
  })
  .run(env.PORT || 8080);
```

The website host reads `website()` and `run()`. The desktop tool additionally
reads `desktop()`. A desktop-only manifest may omit both website calls.

Do not mix canonical and legacy equivalents in one desktop object:

```javascript
// Invalid: native and chrome express the same decision.
app.desktop({ native: true, chrome: "native" });

// Invalid: width is declared both flat and inside legacy window.
app.desktop({ width: 1280, window: { width: 1280 } });
```

## Mobile

`app.mobile()` is consumed by native mobile build and shell tooling. A
versioned mobile object uses a strict schema: unknown fields and unsupported
permissions fail validation rather than being silently ignored.

Mobile identity currently comes from the shared `app.title()` and `app.icon()`
fields. Desktop manifests should use identity inside `app.desktop()`.

### Smallest useful mobile app

Start with identity, one stable application ID, and the site the shell opens:

```javascript
import { app } from "kitwork";

app
  .title("Pocket Notes")
  .icon("./assets/app-icon.png")
  .mobile({
    version: 10,
    id: "io.kitwork.notes",
    start: {
      domain: "notes.example.com"
    }
  });
```

This is a complete strict mobile declaration. The resolver supplies:

```text
versionName: "0.1.0"
versionCode: 1
start.path: "/"
orientation: "auto"
theme.mode: "system"
theme.color: "#09090b"
permissions: []
```

Do not add every optional field on day one. Add a field when the application
has made that decision.

### Website and mobile together

The website host and native builder consume different parts of the same
manifest:

```javascript
import { app, env } from "kitwork";

app
  .title("Pocket Notes")
  .icon("./assets/app-icon.png")
  .website("notes.example.com")
  .mobile({
    version: 10,
    id: "io.kitwork.notes",
    start: {
      domain: "notes.example.com"
    }
  })
  .run(env.PORT || 8080);
```

The current strict schema does not infer `start.domain` from `website()`, so it
is repeated intentionally. A future authoring layer may normalize that
duplication without weakening the encoded native manifest.

### Full mobile configuration

Add release metadata, presentation, and capabilities only when needed:

```javascript
import { app } from "kitwork";

app
  .title("Pocket Notes")
  .icon("./assets/app-icon.png")
  .mobile({
    version: 10,
    id: "io.kitwork.notes",
    versionName: "1.0.0",
    versionCode: 1,
    start: {
      domain: "notes.kitwork.localhost",
      path: "/"
    },
    orientation: "portrait",
    theme: {
      mode: "system",
      color: "#635bff"
    },
    permissions: [
      "clipboard.writeText",
      "share.open",
      "network.request"
    ],
    network: {
      origins: ["https://api.example.com"]
    }
  });
```

Important mobile rules:

- `orientation` is `"auto"`, `"portrait"`, or `"landscape"`.
- Network permissions and a bounded `network.origins` policy must be declared
  together.
- Native network origins are canonical public HTTPS origins without ports,
  paths, credentials, query strings, or fragments.
- Deep-link fields and notification permissions are version-coupled.
- A mobile-only manifest is valid, but `engine.Run()` has no HTTP work to do;
  native tooling is the intended consumer.

The mobile schema is independently versioned. Native tooling must reject a
manifest version it does not understand instead of assuming newer authority.

## KitDB owned by the app

`app.database()` creates a KitDB resource owned by this process. It is not an
external connection. The same engine can be exposed through up to three
adapters:

1. native in-process access;
2. the PostgreSQL-compatible wire protocol;
3. KitSQL on the existing Kitwork website listener.

These are adapters over one database, not independent copies.

### Embedded single-file database

```javascript
app.database({
  path: "./data/shop.kitdb",
  alias: "shop",
  memory: "128mb",
  concurrency: 4,
  warm: true
});
```

Tenant code selects it by declared alias:

```javascript
const shop = database.connection("shop");
```

Without an explicit alias, a single file is published as `default`.

### Managed database root

```javascript
app.database({
  path: "./.database/",
  memory: "256mb",
  concurrency: 8,
  warm: ["system", "analytics"]
});
```

A directory is a managed root. Each logical database is exposed by its own
name, so `alias` is not valid on the root itself. `warm: true` is only valid for
one file; a managed root requires an explicit database name or list.

### PostgreSQL-compatible listener

```javascript
app.database({
  path: "./.database/",
  host: "127.0.0.1",
  port: 5455,
  user: "kitdb",
  password: env.require("KITDB_PASSWORD"),
  memory: "256mb",
  concurrency: "auto",
  warm: ["system"]
});
```

`port` requires a password. The default host is `127.0.0.1` and the default
user is `kitdb`.

### KitSQL over the website listener

```javascript
app
  .database({
    path: "./data/shop.kitdb",
    alias: "shop",
    kitsql: true,
    password: env.require("KITSQL_PASSWORD")
  })
  .website({
    hostname: "data.example.com",
    allowLocal: env.ALLOW_LOCAL || false
  })
  .run(env.PORT || 8080);
```

`kitsql: true` requires a website surface and a password. Remote KitSQL uses
HTTPS; cleartext access is loopback-only.

### Query cache policy

One duration for supported query classes:

```javascript
app.database({
  path: "./data/shop.kitdb",
  cache: "1m"
});
```

Different policies with explicit bounds:

```javascript
app.database({
  path: "./data/shop.kitdb",
  cache: {
    select: "2m",
    search: "30s",
    analytics: "5m",
    memory: "32mb",
    entries: 10000,
    result: "1mb"
  }
});
```

`cache: true` is rejected because it does not define a lifetime. Use `false`,
a duration, or an object. Writes invalidate results tied to an earlier KitDB
transaction.

## External connections

Connection methods declare resources the application consumes. Credentials
stay in the host manifest; tenant code receives only a declared alias.

### PostgreSQL

```javascript
app.postgresql({
  alias: "system",
  host: env.DB_HOST || "127.0.0.1",
  port: env.DB_PORT || 5432,
  user: env.DB_USER || "postgres",
  password: env.require("DB_PASSWORD"),
  name: env.DB_NAME || "kitwork",
  sslmode: env.DB_SSL || "require"
});
```

Or use a configured URL:

```javascript
app.postgresql({
  alias: "system",
  url: env.require("DATABASE_URL")
});
```

### SQLite

```javascript
app.sqlite({
  alias: "legacy",
  name: "./data/legacy.sqlite"
});
```

### Generic connector

```javascript
app.connect({
  alias: "warehouse",
  type: "postgres",
  url: env.require("WAREHOUSE_URL")
});
```

Provider methods currently include:

```javascript
app.postgresql({ alias: "main", url: env.POSTGRESQL_URL });
app.mysql({ alias: "legacy", url: env.MYSQL_URL });
app.sqlite({ alias: "local", name: "./data/local.sqlite" });
app.redis({ alias: "cache", url: env.REDIS_URL });
app.connect({ alias: "other", type: "postgres", url: env.OTHER_URL });
```

Runtime availability still depends on the connector being linked into the
engine distribution. PostgreSQL, KitSQL, and SQLite are included in the current
engine; an unavailable connector fails clearly rather than pretending to be
connected.

Tenant code uses the alias only:

```javascript
const system = database.connection("system");
const available = database.connected("system");
```

Owned KitDB databases and external connections share one case-insensitive
alias namespace. Duplicate aliases fail during startup. A managed KitDB root
checks dynamic member-name collisions when the alias is resolved.

## Gateway

`app.gateway()` is a host-owned reverse proxy for trusted HTTP or HTTPS
backends. Gateway traffic does not enter a tenant VM.

### One backend

```javascript
app.gateway({
  domain: "api.example.com",
  target: "http://10.0.0.12:3000",
  health: "/health"
});
```

### Weighted backend pool

```javascript
app.gateway(
  {
    domain: "api.example.com",
    target: "http://node-a:3000",
    weight: 2,
    health: "/health"
  },
  {
    domain: "api.example.com",
    target: "http://node-b:3000",
    weight: 1,
    health: "/health"
  }
);
```

Targets with the same domain form one immutable pool. A missing weight defaults
to `1`. Equal weights behave as round-robin; unequal weights use a prepared
smooth weighted schedule. Passive failures are tracked for every backend.
Declaring `health` additionally enables bounded active checks.

`target` must be an absolute HTTP or HTTPS URL without credentials or a
fragment. `health`, when present, must be an absolute path such as `/health`.

### Gateway-only host

```javascript
app.gateway(
  { target: "http://node-a:3000" },
  { target: "http://node-b:3000" }
);
```

A target without `domain` belongs to the default pool and is valid only when no
website surface exists. This lets a gateway-only manifest avoid an `app/` or
`apps/` root and prevents a catch-all gateway from shadowing website routes.

### Website plus domain gateway

```javascript
app
  .root("app")
  .gateway(
    { domain: "api.example.com", target: "http://api-a:3000" },
    { domain: "api.example.com", target: "http://api-b:3000" }
  )
  .website({
    hostname: "example.com",
    domains: ["example.com", "api.example.com"]
  })
  .run(8080);
```

Exact gateway domains are dispatched before tenant resolution. Website routes
continue to handle other configured domains.

## Forward proxy

`app.proxy()` opens host-owned HTTP or SOCKS5 forward-proxy listeners. It is
different from `app.gateway()` and route-level `router.proxy()`:

| API | Direction | Purpose |
| --- | --- | --- |
| `app.gateway()` | Internet -> your backends | Reverse proxy and load balancing |
| `app.proxy()` | Client -> external destination | HTTP/SOCKS5 forward-proxy service |
| `router.proxy()` | One route -> one upstream | Application route behavior |

### Local HTTP proxy

```javascript
app.proxy({
  port: 8888
});
```

The default protocol is `http`; the default host is `127.0.0.1`.

### Authenticated public listeners

```javascript
app.proxy(
  {
    protocol: "http",
    host: "0.0.0.0",
    port: 8888,
    username: env.require("PROXY_USERNAME"),
    password: env.require("PROXY_PASSWORD")
  },
  {
    protocol: "socks5",
    host: "0.0.0.0",
    port: 1080,
    username: env.require("PROXY_USERNAME"),
    password: env.require("PROXY_PASSWORD")
  }
);
```

Proxy invariants:

- `protocol` is `http` or `socks5`;
- `port` must be between 1 and 65535;
- username and password must be configured together;
- a non-loopback listener requires authentication;
- duplicate listener addresses are rejected;
- at most 32 listeners may be declared;
- each listener owns a bounded number of active connections;
- private destinations require `allowPrivate: true`;
- link-local, multicast, and special-purpose destinations remain blocked.

HTTP supports ordinary absolute-form requests and HTTPS `CONNECT`. SOCKS5
supports CONNECT; BIND and UDP ASSOCIATE are outside the current contract.

HTTP Basic and SOCKS5 password authentication do not encrypt the client-to-
proxy connection. A public listener still needs a trusted network, VPN, or TLS
tunnel.

Proxy-only manifests do not require an application root:

```javascript
import { app, env } from "kitwork";

app.proxy({
  protocol: "socks5",
  host: "0.0.0.0",
  port: 1080,
  username: env.require("PROXY_USERNAME"),
  password: env.require("PROXY_PASSWORD")
});
```

## Logger

```javascript
app.logger({
  level: "info",
  format: "json",
  logfile: "./logs/kitwork.log",
  errorfile: "./logs/error.log",
  max_size: 20,
  max_backups: 5,
  max_age: 30,
  compress: true,
  console: true
});
```

| Option | Meaning |
| --- | --- |
| `level` | Minimum log level. |
| `format` | Text or structured output format supported by the logger. |
| `logfile` | General log destination. |
| `errorfile` | Error log destination. |
| `max_size` | Rotation threshold in megabytes. |
| `max_backups` | Number of rotated files retained. |
| `max_age` | Retention in days. |
| `compress` | Compress rotated files. |
| `console` | Explicitly enable or disable console output. |

## Composition recipes

### Multi-tenant host with system PostgreSQL and owned KitDB

```javascript
import { app, env } from "kitwork";

app
  .root("apps")

  .postgresql({
    alias: "system",
    host: env.DB_HOST,
    port: env.DB_PORT || 5432,
    user: env.DB_USER,
    password: env.require("DB_PASSWORD"),
    name: env.DB_NAME || "kitwork",
    sslmode: env.DB_SSL || "require"
  })

  .database({
    path: "./.database/",
    memory: "256mb",
    concurrency: 8,
    warm: ["analytics"]
  })

  .website({
    hostname: "kitwork.io",
    allowLocal: env.ALLOW_LOCAL || false,
    trustProxy: env.TRUST_PROXY || false,
    bytecodeCache: true,
    rateLimit: {
      rate: 4000,
      ip: 120,
      browser: 240,
      user: 240,
      period: "1s"
    }
  })

  .run(env.PORT || 8080);
```

### Website, desktop shell, gateway, and local proxy

```javascript
import { app, env } from "kitwork";

app
  .root("app")

  .gateway(
    { domain: "api.example.com", target: "http://api-a:3000", weight: 2 },
    { domain: "api.example.com", target: "http://api-b:3000", weight: 1 }
  )

  .proxy({
    protocol: "http",
    host: "127.0.0.1",
    port: 8888
  })

  .website({
    hostname: "example.com",
    domains: ["example.com", "api.example.com"],
    allowLocal: env.ALLOW_LOCAL || false
  })

  .desktop({
    name: "Example Studio",
    icon: "./assets/icon.png",
    width: 1280,
    height: 820,
    minWidth: 960,
    minHeight: 640,
    native: false,
    resizable: true,
    center: true
  })

  .run(env.PORT || 8080);
```

This composition is valid because the gateway uses an exact domain rather than
a default pool, and the forward proxy listens on a different port.

## Lifecycle

Kitwork handles a manifest in phases:

1. Compile and evaluate `app.kitwork.js` in the setup VM.
2. Validate the complete surface and connection declarations.
3. Resolve the application root when a website or owned database needs it.
4. Construct external connections and host-owned resources.
5. Start owned database and forward-proxy listeners.
6. Construct the website or gateway request boundary.
7. Prepare selected website generations.
8. Publish readiness and begin serving.
9. Drain listeners and runtime owners in dependency order during shutdown.

A failure before readiness prevents the host from accepting traffic. An
unexpected listener failure after startup is reported to the process lifecycle
instead of leaving a partially running host.

`kitwork check` evaluates the same manifest and validates website content
without opening listeners. Desktop-only and mobile-only manifests are valid for
their native tools even though the cloud host has nothing to serve.

## Canonical API reference

### Host configuration

| Method | Purpose |
| --- | --- |
| `app.root("app" or "apps")` | Select the application ownership layout. |
| `app.logger(options)` | Configure process logging and rotation. |

### Surfaces

| Method | Creates or declares |
| --- | --- |
| `app.website(hostname or options)` | Filesystem-routed websites and APIs. |
| `app.desktop(options)` | Desktop identity and window behavior. |
| `app.mobile(options)` | Versioned mobile shell contract. |
| `app.database({ path, ...options })` | App-owned KitDB file or managed root. |
| `app.gateway(target, ...)` | Reverse proxy and trusted backend pools. |
| `app.proxy(listener, ...)` | HTTP or SOCKS5 forward-proxy listeners. |

### Connections

| Method | Resource consumed by the app |
| --- | --- |
| `app.postgresql({ alias, ...options })` | External PostgreSQL connection. |
| `app.mysql({ alias, ...options })` | External MySQL connection when linked. |
| `app.sqlite({ alias, name })` | Host-configured SQLite connection. |
| `app.redis({ alias, ...options })` | Redis connection when linked. |
| `app.connect({ alias, type, ...options })` | Generic declared external connector. |

### Lifecycle

| Method | Purpose |
| --- | --- |
| `app.prewarm()` | Prepare the primary website hostname. |
| `app.prewarm(domain, ...)` | Prepare exact website domains. |
| `app.prewarm("*")` | Prepare every discovered site. |
| `app.prewarm(false)` | Keep website loading request-driven. |
| `app.run(port?)` | Start the website server; default port is `8080`. |

## Compatibility API

Compatibility APIs remain readable so existing deployments can migrate
without an all-at-once rewrite. New examples and new applications should use
the canonical forms above.

| Compatibility API | Canonical replacement |
| --- | --- |
| `app.web(options)` | `app.website(options).run(port)` |
| `app.port(port)` | `app.run(port)` |
| `app.hostname(host)` | `app.website({ hostname: host })` |
| `app.hotReload(value)` | `app.website({ hotReload: value })` |
| `app.allowLocal(value)` | `app.website({ allowLocal: value })` |
| `app.trustProxy(value)` | `app.website({ trustProxy: value })` |
| `app.redirects(map)` | `app.website({ redirects: map })` |
| `app.canonical(mode)` | `app.website({ canonical: mode })` |
| `app.rateLimit(options)` | `app.website({ rateLimit: options })` |
| `app.chrome(mode)` | `app.desktop({ native: mode === "native" })` |
| `app.window(options)` | Flat fields inside `app.desktop(options)` |
| `desktop({ chrome, window })` | Flat `desktop({ native, width, ... })` |
| `app.databases(list)` | Provider methods or owned `app.database(...)` |
| `app.database({ alias, type, ... })` | `app.postgresql`, `sqlite`, or `connect` |
| `server.run(...)` | `app.run(...)` |

`app.title()` and `app.icon()` remain required identity inputs for the current
mobile schema and compatibility fallbacks for older desktop tooling. They are
not web-page metadata; page titles belong to route/view metadata.

## Common mistakes

### Putting a port inside `website`

```javascript
// Wrong
app.website({ hostname: "example.com", port: 8080 });

// Correct
app.website("example.com").run(8080);
```

### Calling `run` twice

```javascript
// Wrong: run is terminal and may be called once.
app.website("example.com").run(8080);
app.run(9090);
```

### Using a URL as the website hostname

```javascript
// Wrong
app.website("https://example.com");

// Correct
app.website("example.com");
```

### Exposing a proxy without authentication

```javascript
// Wrong: non-loopback listener has no credentials.
app.proxy({ host: "0.0.0.0", port: 8888 });

// Correct
app.proxy({
  host: "0.0.0.0",
  port: 8888,
  username: env.require("PROXY_USERNAME"),
  password: env.require("PROXY_PASSWORD")
});
```

### Giving a managed KitDB root one alias

```javascript
// Wrong: directories expose each contained database by its own name.
app.database({ path: "./.database/", alias: "main" });

// Correct
app.database({ path: "./.database/", warm: ["main"] });
```

### Mixing a default gateway with a website

```javascript
// Wrong: a domainless pool would shadow website routing.
app.gateway({ target: "http://node-a:3000" })
  .website("example.com")
  .run(8080);

// Correct: scope the gateway to an exact domain.
app.gateway({
  domain: "api.example.com",
  target: "http://node-a:3000"
}).website({
  hostname: "example.com",
  domains: ["example.com", "api.example.com"]
}).run(8080);
```

## Change checklist

The app manifest is a public contract. Adding or changing an API requires all
of the following to stay synchronized:

1. parser and runtime implementation;
2. validation and compatibility behavior;
3. focused manifest tests;
4. `kitwork.d.ts` editor types;
5. this document;
6. the public website reference and examples.

The implementation is authoritative for runtime behavior. This document is the
canonical human guide, while `kitwork.d.ts` is the editor-facing shape of the
same contract.
