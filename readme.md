# Kitwork Engine

Kitwork runs an application from a folder, and is built so that the host stays up when the code in
that folder was written by someone else, or by an AI.

Kitwork treats an application as a blueprint. The `.ui` and `.work` files say what the application
is: its pages, its routes, its data, what may happen on it. They do not say where any of it runs.
The engine compiles that blueprint into one intermediate form, verified and fingerprinted bytecode
for the logic and a compiled page tree for the interface, and every target reads that form, never
the source: the server that renders HTML today, the kernel in the browser, the desktop and mobile
shells. A new target is a new reader of the same form, not a rewrite of the application. The
language is bounded so that form can be run for a stranger, and the engine decides itself which
part runs on the server and which in the browser.

Everything in the engine exists to keep a small set of promises to the host: a capability can be
used only where it was granted; logic runs in a sandbox; one tenant cannot reach another; whatever
crosses a boundary is checked and limited; and every step costs energy, so nothing runs forever.
The subset of JavaScript is the first of those fences, not a limitation: a loop must count toward
a bound or walk a list, there is no `try` to swallow an error, and what the grammar cannot catch,
the energy budget does. Files and outbound requests are reached only through the engine, which
confines them to the site that asked; databases are reached only by an alias the host declared.
One process can therefore hold many applications, each request running in a machine leased for it
and wiped afterwards, without a container or a second runtime between them.

Kitwork is for one person who owns the application and the machine it runs on, and runs more of
them than one person is supposed to manage alone: sites, APIs, data and background work, written
by them, by someone they trust less, or by an AI. It is not a Node.js replacement and does not run
npm packages, and it is the wrong tool for long computations inside a request.

This is a pre-1.0 engine in active stabilization. The author's own sites run on it. What is done
and what is not is listed below, and nothing in this file claims more than the tests prove.

## Contents

- An application
- Run it
- The manifest
- The language
- Pages
- Routes
- Data
- Background work
- The page's own assets
- The browser side
- Network
- What happens to a request
- What the engine enforces
- What decides every choice
- What is not done
- Where it is going
- Verify it
- The repository
- Read next
- License

## An application

```text
app/
  router.work                 the rules of the site and what / shows
  page.ui                     the frame around every page
  content.ui                  the page at /
  api/hello/router.work       the route /api/hello, JSON only
  api/notes/router.work       the route /api/notes, writes to the site's own database
  assets/                     static files, served as they are at /assets/…
  .data/                      made by the engine: app.db and whatever else the site keeps
```

The folder tree is the URL space. A folder is a route; a `router.work` inside it says what the
route does; a `content.ui` beside it is the page. Parameters are folder names: `{id[number]}`,
`{slug[string]}`, `{slug(regex)}` and `{...rest}`.

```js
// app/router.work
import { router } from "kitwork";

router.language("en");

router.view((ctx) => {
  const greeting = "Hello from a folder";
  ctx.title("Example");
});
```

```html
<!-- app/content.ui -->
<content padding="6" gap="4">
  <text heading="1">{{ greeting }}</text>
  <text color="ink-30">Rendered on the server from a .ui file.</text>
</content>
```

```js
// app/api/notes/router.work
import { router, sqlite } from "kitwork";

router.get((ctx) => {
  sqlite.exec("CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, text TEXT)");
  sqlite.table("notes").create({ text: ctx.query("text") || "hello" });
  return ctx.json(sqlite.table("notes").sort("id", "asc").list());
});
```

A view declares the data a page reads; a `const` is printed into the HTML and never shipped as
script. The `.ui` file names elements by what they are (`content`, `text`, `button`, `card`) and
describes them by intent (`padding`, `gap`, `heading`, `color`); the engine chooses the HTML tag,
writes the CSS those attributes need and nothing else, and checks at compile time that every text
reads against its background. A `page.ui` with `<content />` in it is the frame every page is
placed in. The database the notes route writes to is `app/.data/app.db`, made on first use: copy the
folder and the data goes with it.

Source files are never served. Asking for `/router.work` or `/content.ui` is a 404.

## Run it

The smallest host is one Go file and one manifest.

```go
// main.go
package main

import (
	"fmt"
	"os"

	"github.com/kitwork/engine"
)

func main() {
	if err := engine.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "kitwork:", err)
		os.Exit(1)
	}
}
```

```js
// app.kitwork.js
import { app, env } from "kitwork";

app
  .root("app")
  .website({ hostname: "example.localhost", allowLocal: true, hotReload: true })
  .run(env.PORT || 8080);
```

```bash
go run .
```

```text
$ curl "http://localhost:8080/api/notes?text=first"
[{"id":1,"text":"first"}]
```

With `allowLocal` on there is no TLS, `localhost` means the configured hostname, and
`<domain>.localhost` reaches a site by its folder name. With `allowLocal` off, the host also
answers on 443 with certificates it obtains itself through ACME and keeps under `./certs`. With
`hotReload` on, saving a file prepares a new generation and swaps it in; without it, restart.

Put two tenants under `apps/` and break one of them with a `while`. The broken site answers an
error, and the log names the line that wrote it; the other site keeps answering; the host never
went down. That is the first promise of this file in one test.

The reference host in the Kitwork repository wraps this library in a command line: `new` scaffolds
a tenant, `dev` runs with `allowLocal` on, `check` compiles every site through the production
pipeline and reports every failure without opening a listener, `profile` reports the bytecode of
every program, and `inspect` explains one program instruction by instruction.

## The manifest

The manifest is written in the same subset and evaluated once at boot, in a machine that knows only
`app` and `env`; it keeps the `.kitwork.js` name for now. It declares four kinds of things, and the
engine starts only what was declared.

```js
import { app, env } from "kitwork";

app
  .root("apps")
  .logger({ level: env.LOG_LEVEL || "info", format: "json", logfile: "logs/app.log" })
  .website({
    hostname: "example.com",
    domains: ["example.com", "www.example.com"],
    canonical: "apex",
    rateLimit: { rate: 2000, ip: 120, browser: 240, period: "1s" },
    bytecodeCache: true
  })
  .database("./.database/", { memory: "64mb", warm: ["shop"] })
  .postgresql("system", { host: env.DB_HOST, port: 5432, user: env.DB_USER, password: env.DB_PASSWORD, name: "kitwork" })
  .desktop({ name: "Example", width: 1200, height: 820 })
  .prewarm("*")
  .run(env.PORT || 8080);
```

- Host configuration: `root` chooses the layout, `logger` the log level, format and rotation.
- Surfaces, what the app offers: `website` (hostname, domains, canonical host, redirects, rate
  limits, trust of a proxy's forwarded headers, bytecode cache, hot reload), `database` (a KitDB file
  or a managed root the app owns, optionally on a PostgreSQL wire port), `desktop` and `mobile`
  (declared here, opened by a separate shell), `gateway` (weighted reverse-proxy targets), `proxy`
  (HTTP or SOCKS5 forward-proxy listeners).
- Connections, what the app consumes, always alias first: `postgresql`, `sqlite`, `connect`; `mysql`
  and `redis` are accepted by the manifest but have no driver linked in this build.
- Lifecycle: `prewarm` (the primary hostname by default, named domains, or `"*"` for every site) and
  `run`, the last call, which opens the listener.

`env.KEY` reads the host's `.env` and then the real environment, which wins, and coerces numbers and
booleans; `env.require("KEY")` stops the boot when the key is missing. Do not name a key after an
operating-system variable such as `PATH` or `HOME`. A manifest with no `website` and no `run` starts
no listener: a `database` surface alone is a complete manifest.

Three layouts are recognised: one site at the root (`app/router.work`), one application with a
folder per domain (`app/<domain>/`), or many tenants (`apps/<identity>/<domain>/`). Within an
application the domains share its declared connections and background work; each domain keeps its
own `.data` and its own `.env`. `docs/APP.md` lists every option.

## The language

What is accepted behaves as it does in JavaScript, and where Kitwork differs on purpose (one `null`
for both `null` and `undefined`, a division by zero that gives `null`) the language guide says so:
`const` and `let` with block scope, arrow functions and `function`, objects and arrays,
destructuring, template strings, `switch`, `?.` and `??`, `typeof`, `++`. Imports are native:
`"kitwork"` gives the host capabilities, a relative path gives another file, and the engine
bundles them itself. There are no promises, no `async` and no `await`: the language is
synchronous, and the engine waits for I/O itself.

What is removed is removed at compile time, most of it with a message that says what to write
instead:

- `while`, `do`, and any `for` that is not a counted loop or a walk over a list;
- `try`, `catch`, `finally`, `throw`;
- `class`, labels, regular expression literals;
- an unbounded program: a run has an energy budget, and the call stack is capped at 64 frames.

A failure is a value. Anything that goes wrong inside a call, from a query the database refused
to a request that got no answer, is a failure: unread, it stops the handler and is logged; read
with `safe`, it is data:

```js
// app/api/upstream/router.work
import { router, http, safe } from "kitwork";

router.get((ctx) => {
  const { value: response, error } = safe(() => http.timeout(2000).get("https://example.com/feed"));
  if (error) return ctx.status(502).json({ code: error.code, message: error.message });
  return ctx.json(response.json());
});
```

`error` is `null` or `{ message, code, at }`, where `code` comes from a closed set (`NETWORK`,
`TIMEOUT`, `DATABASE`, `CONFLICT`, `DENIED`, …) and `at` is the file, line and column of the
failure. The limits of the host, energy, cancellation and stack depth, cannot be caught.

The compiler and the `.ui` checker speak Vietnamese today. A `while` anywhere in a site stops that
site from starting, at the line that wrote it:

```text
compile router api/loop/router.work: router.work:5:3: Kitwork không hỗ trợ vòng điều kiện tuỳ ý 'while' …
```

What `"kitwork"` gives a `.work` file: `router` and `safe`; `database`, `sqlite` and `collection`
for data; `http`, `fetch` and `webhook` for the network; `file`, `path` and `cache`; `env` and
`log`; `go` for detached work; `jwt`, `qrcode`, `napas`, `shortbase`, `insights`, `palette`,
`pigment` and `playground` as helpers. The globals are the familiar ones: `Math`, `Date`, `JSON`,
`console`, `Object`, `Number`, `BigInt`, `String`, `Boolean`, `Array`, `parseInt`, `parseFloat`,
plus `fail` and `safe`. The host repository's `kitwork.d.ts` describes each for an editor.

## Pages

A page is a `.ui` file. It is close to HTML and is not HTML: the elements are Kitwork's, named by
role, and the attributes describe intent, never shape. The engine writes the HTML, the CSS and the
`<head>`.

```text
app/
  page.ui          <page> … <header /> <content /> <footer /> </page>: the frame
  header.ui        a region, called by its tag from page.ui; nearest wins down the tree
  navbar.ui        a region: links only
  footer.ui
  content.ui       the page of this folder, placed into <content />
  notfound.ui      what <content /> shows when no route matches
  surface-card.ui  a piece with no logic, called as <surface-card for="…" />
  docs/content.ui  the page at /docs, framed by the nearest page.ui above it
```

The vocabulary, in families: `page`, `section`, `box`, `grid`, `card` for layout; `text`, `code`,
`quote`, `time`, `link`, `icon`, `image` for content; `list` and `item`; `input`, `button`,
`field`, `label`, `hint`, `error` for forms; `accordion`, `breadcrumb`, `popover`, `reader` as
elements with behaviour of their own. Layout is attributes on any container: `flex="row"`, `gap`,
`align`, `space`, `wrap`, `padding` and its edges, `container` and `max-width` on a width scale
(`small` to `huge`), `pin="top"` to hold a frame at the screen edge while the page scrolls. There
is no `display`, no `position`, no `z-index`, no `margin`, no `span`: each of those names a shape
on one target, and the measure for every word in `.ui` is whether it still means something when
the target is not the web.

Values and structure:

```html
<grid if="products.length > 0" columns="1" small:columns="2" gap="4">
  <product-card for="product of products; key: product.id" />
</grid>
<text if="products.length == 0">Nothing on the menu today.</text>
<text show="open">Shown or hidden, state kept.</text>
```

`{{ }}` inserts a value, escaped; `if` removes an element, `show` hides it; `for` walks a list with
its key in the same attribute. A prefix in front of an attribute makes it conditional on width
(`small:` `medium:` `large:` `wide:` `huge:`) or on a state (`hover:` `pressed:` `focus:`
`disabled:` `scrolled:`): `scrolled:shadow="small"` on a pinned header is a shadow that appears
once the page has scrolled.

Three forms of markup, and only three: an element of Kitwork is written by its tag; a piece with no
logic is a `.ui` file of the site, called by its file name as a tag and reading the context where it
is called; a thing with logic is a component, written as `component="name"` on an element
(`<box component="dropdown" as="$menu">`). Has logic, then component; only markup, then layout.

The folder's `router.work` sets what flows down to every page under it: `router.colors` (the
palette as tokens, each a colour or a `{ light, dark }` pair), `router.fonts` (typefaces by role),
`router.appearance` (`system`, `light` or `dark` before the visitor chooses), `router.highlight`
(the colours of code), `router.gradients`, `router.language`, `router.meta`, `router.favicon`,
`router.reload(false)` with `.transition("fade")` for moving between pages without a reload, and
`router.bundle` for how CSS and script are delivered. Colours are named by role (`brand`, `canvas`,
`paper`, `ink`, `line`) and shaded by arithmetic (`ink-30` lighter, `brand+15` darker, `ink/80`
translucent); every text is measured against its background in both modes at compile time, and a
pair under the contrast threshold is reported at its file, line and column. Code inside `<code
language="go">` is highlighted on the server. A `<page background="cirrus">` paints a gradient
backdrop; `material="glass"` on a frame is frosted glass over whatever scrolls beneath it.

Static files are served from `assets/` at the same path. Source files and dot-folders are never
reachable from outside: not `.work`, not `.ui`, not `.data`.

The first way to write a page, `.kitwork.html` templates with `{{ }}` expressions, `if`, `for` and
`let`, assembled from a shell and named slots (`@page`, `@header`, `@navbar`, `@footer` and the
rest) found by walking up the folder tree, with `data-kit-*` attributes read by the browser kernel,
is still served and is being rewritten into `.ui`.

## Routes

A folder's `router.work` declares the methods of its own path: `get`, `post`, `put`, `patch`,
`delete`, `notfound`, `proxy`, and `view` for a page. Each method is a chain, read top to bottom:

```js
import { router, sqlite } from "kitwork";

router
  .get((ctx) => ctx.json({ items: sqlite.table("items").list() }))
  .guard((ctx) => Boolean(ctx.cookie("session")))
  .cache("5m")
  .limit({ rate: 30, per: "1m" });
```

- On a method: `guard` runs first and may block or answer; `handle` or the callable itself;
  `success`, `error` and `finally` after it; `cache(expiry)` keeps the response in RAM for the
  generation; `persist(expiry)` keeps it on disk for the site; both are keyed by method, path and
  query, so neither suits a personalised page; `limit` adds a rate rule.
- On a folder, inherited by everything below it: `guard`, `error`, `notfound`, `meta`, `title`,
  `description`, `language`, `favicon`, `assets`, `rateLimit`, `database` (the alias `ctx.database`
  reads), and the cached declarations `rss`, `sitemap` and `proxy`.
- `ctx` carries the request: `params`, `query`, `header`, `cookie`, `body`, `ip`, `method`,
  `path`; and the answer: `json`, `text`, `html`, `file`, `redirect`, `status`, `type`,
  `setCookie`; and for a page, `bind`, `title`, `meta`. The `error` hook receives
  `{ message, code, at }`.

Match precedence is exact, typed, regex, plain dynamic, then splat. Rate limits count per IP, per
client, per user or site-wide, by second, minute, hour or day; loopback and private addresses are
exempt. Without a `router.assets` allow-list, every non-source file under a template site is
public.

## Data

Four doors, each explicit:

- `sqlite`: the site's own file at `.data/app.db`, confined to `.data`; `sqlite.open("other.db")`
  for another file there, `sqlite.memory()` for none; `exec` for DDL, `table(name)` for the builder.
- `database.connect("alias")` or `router.database("alias")` with `ctx.database`: a connection the
  manifest declared, PostgreSQL or SQLite, or a database inside the KitDB root the app owns. A site
  never supplies a connection string.
- `database.entity()`: the system database, every query scoped to the app's identity by the engine.
- `collection.open("name")`: Markdown documents under `_collection/<name>`, with a full-text index.

The query builder is the same on every door: `table`, `select`, `where` (`("age", ">", 18)`, an
object, or a lambda), `and`, `or`, `in`, `isNull`, `orderBy` or `sort`, `limit`, `skip`, joins,
`groupBy`, `having`; terminals `first`, `find`, `list`, `count`, `sum`, `avg`, `max`, `exists`;
writes `create`, `update`, `delete` and `remove`. Values travel as parameters. An update or a delete
that names no row is refused.

KitDB is an embedded relational database written in Go and incubated in this repository, with its
own storage, write-ahead log, checkpoints, verified backup and point-in-time recovery, one writer
per database, a loopback-only cleartext PostgreSQL wire profile so ordinary tools can connect, and
a 1.0 release contract still at release-candidate stage. The app's `database` surface opens one file
or a managed root of them; `kitdb/README.md` says exactly what is supported and what is not.

## Background work

Durable schedules and jobs exist in the engine today, stored in the application's SQLite files or
in the system PostgreSQL when one is connected, with deduplication keys, retry budgets and replay
of failed jobs; a retried handler runs again, so it must be idempotent. How an author declares them
is being redesigned, so this file does not teach it yet. `go(fn)` runs detached work that may be
lost on restart, on purpose. Server-sent events go through a broker that outlives the request:

```js
router.get((ctx, sse) => sse.connect({ channel: "orders", retry: 3000 }));
```

## The page's own assets

The engine writes only what a page uses: its CSS from the attributes it wrote (utility classes in
the Tailwind vocabulary on a template page), icons from the vendored Tabler set and brand marks
from Simple Icons as CSS masks, `@font-face` for the subset fonts it embeds (Google Sans Flex,
Google Sans Code, JetBrains Mono, Outfit, Fira Code), and server-side syntax highlighting (Go,
JavaScript, SQL, bash, YAML and the Kitwork files among them). Nothing is built ahead of time, and a
page never loads anything from a CDN.

Every static file carries an `ETag` and `Last-Modified`; a file whose name carries the hash of its
bytes is cached for a year, any other for an hour in production.

## The browser side

One kernel reads the attributes the engine wrote for a `.ui` page, or the `data-kit-*` attributes
an author wrote on a template page, and mounts named components from the engine's catalogue
(dialog, tabs, toast, calendar, clipboard, …). Each part is one file under `/_kitwork/`, named by
its content hash, delivered only to the pages that use it, cached as immutable. Drive swaps pages in
place instead of reloading, keeping what the frame holds open; a `.ui` site has it on, a template
site asks with `router.reload(false)`.

## Network

Certificates through ACME, host rate limits, canonical host and redirect rules, a weighted
reverse-proxy gateway with health checks, HTTP and SOCKS5 forward proxies that refuse a public bind
without credentials, and a guard that refuses outbound requests to loopback and private addresses
(lifted while `allowLocal` is on). Behind a proxy the engine ignores forwarded headers unless
`trustProxy` says otherwise.

## What happens to a request

```text
source  ->  compiler  ->  verified Program  ->  generation  ->  VM lease  ->  response
```

Every file is compiled before a site is activated, and every Program is verified before it is
published: unknown opcodes, bad jumps, stack underflow and inconsistent control flow are refused
there, not discovered while serving. The route graph, pages, environment and generated assets of
a site are frozen together as one generation. A request pins a generation and leases a machine
from a pool; the machine is wiped before it serves anyone else.

When a file changes and hot reload is on, the engine prepares a complete new generation in the
background and swaps it in atomically. A candidate that fails to compile is discarded and the old
one keeps serving. Static files never enter a machine at all: they are streamed from disk.

```text
Host
  App runtime (identity)     connections, background work, detached work
    Site runtime (domain)    persisted cache, rate limits, event streams
      Generation (version)   compiled routes, pages, env, assets
        Request scope        cancellation, leased VM, request capabilities
```

Every response carries an `X-Request-Id`. A runtime failure is logged with the request, the site,
the generation, the Program fingerprint and the source position.

## What the engine enforces

What a stranger's code cannot do, and the mechanism that stops it:

- Run forever: no `while`, loops bounded by the grammar, an energy budget on every run, a cap on
  call depth.
- Run unverified bytecode: every Program is copied, verified and fingerprinted before publication,
  and a cached artifact is verified again when loaded.
- Keep anything between requests: the machine is wiped before it goes back to the pool, and
  retention is measured by a campaign in the release gate.
- Read another site's files, environment or database file: paths are resolved inside the site,
  `.env` is per site, SQLite is confined to `.data`, and a connection exists only under an alias
  the host declared.
- Reach the host's own network: outbound requests to loopback, private and link-local addresses
  are refused at dial time.
- Inject SQL: values are bound as parameters; an update or a delete with no condition is refused.
- Serve its own source: `.work`, `.ui`, `.kitwork.js` and `.kitwork.html` are never served.
- Flood the host: rate limits per IP, client, user or site; one request id per response for the
  log.

What it does not do yet, said plainly: there is no authorization policy on a request (the seam
exists, nothing ships with it); the PostgreSQL wire profile of KitDB is loopback-only and
cleartext; identifiers in the query builder are quoted, not validated, so a table or column name
must come from your code, never from a request; and the whole design treats a volumetric attack as
the job of the layer in front, not of the engine.

## What decides every choice

The same few rules recur in every design document and every contract test, and they settle most
arguments before they start.

- The blueprint outlives the executor. A word in `.ui` or `.work` may be added, never change its
  meaning; the engine will be replaced more than once.
- The executor must prove itself. A guarantee without a test that goes red when the mechanism is
  switched off is not a guarantee.
- No sealed magic. Where the engine decides, it says what it decided: which function runs where,
  which fields reach the browser, where a component came from.
- Refuse rather than guess. A missing intent, an unknown word, a web-only trick on another target
  is an error with a suggestion, not a silent default.
- Authority flows down, never up. The manifest outranks a folder's router, which outranks a
  component, which outranks a shared function; a file uses only the powers of its own level.
- State outlives machines. Files are the truth, the database is the index and the journal, and a
  query that returns nothing is not evidence that the database is healthy.
- One node before many, the web before native, correct before fast. Each step waits for the
  evidence of the one before it.
- AI proposes, deterministic code commits. A model may write the application or suggest a plan;
  the engine's checks, limits and durability never depend on it.

## What is not done

- The browser half of `.ui`. A `click="count = count + 1"` on a button compiles, and nothing
  happens in the browser yet: events are parsed and dropped, two-way binding, `var` state and the
  generated page script are being built. `check` and `profile` do not read `router.work` yet. The
  specification, `docs/KITWORK_UI.md` in the Kitwork repository, is a design document, not an API.
- The intermediate form of the interface. The bytecode of the logic is a frozen contract, with a
  version, a fingerprint and an archive of old programs that must still run; the compiled page tree
  is not yet, and changes with the engine.
- The tunnel. Server functions called from the page by name, with the checks that make that safe.
  Designed, not implemented. The earlier idea of shipping client logic to the server was dropped.
- Background work as an author writes it. Today it lives in two folders, `_cron/` and `_queue/`,
  one `.kitwork.js` file per schedule or handler; that surface is being rethought and will change.
- Clustering. `CLUSTER.MD` and `backbone.md` are designs. Nothing coordinates two engine processes
  today; only the PostgreSQL-backed schedule and queue stores were written with that in mind.
- Desktop and mobile. The manifest declares them; the desktop declaration is validated at boot and
  the mobile one by the native build tooling. A separate shell, not in this repository, opens the
  window.
- Authorization on the request. The engine has the seam for a host to attach a principal and its
  permissions to a request; no policy ships with it, and `database.system()` stays inert until one
  does.
- A standalone `kitwork` binary. This repository is the library; the command line lives in the host.

## Where it is going

In this order, and each step only after the one before it has evidence.

- Near. The `.ui` blueprint running end to end in the browser; the tunnel; values kept in step with
  the server through KitDB's ordered history; then the old templates, directives and folders
  removed in one sweep.
- Middle. A second renderer, small and real, to prove the vocabulary carries no shape of HTML; a
  native bridge granted by capability; KitDB embedded in an app as its replica; nodes composed, each
  owning a bounded part, with no shared giant database.
- Far. An engine written in another language that is valid because it passes the same contract
  suite; renderers for native widgets or a canvas, some of them written by a model and trusted the
  same way; and the blueprint itself as what an agent reads to understand and operate an
  application, the way a screen reader reads the accessibility tree.

## Verify it

Go 1.26 is required.

```bash
go build ./... && go vet ./... && go test ./... && go test -race ./...
```

```bash
go run ./cmd/releasegate --mode verify --report .artifacts/verify.json
```

The verification gate loads a committed archive of compiled Programs and requires each to decode,
verify, execute and re-encode unchanged; runs a corpus of malformed Programs and execution faults
that must be rejected or recovered deterministically; compiles the language conformance corpus
in fresh, reused and pooled machines; and runs on Windows and Linux in CI, with the browser proofs
on Linux. The release gate adds multi-process cache and memory retention campaigns; a 24-hour
canary is run separately before a release. Some browser proofs skip when Chrome or Node is not
installed. `docs/RELEASE.md` is the procedure; `docs/STABILITY.md` is the contract every change is
held to.

Performance is recorded, not advertised. `BENCHMARK.md` holds the measurements with the machine and
the command that produced each one, and the rule for comparing them: same benchmark, same payload,
same machine state, five runs. Allocation budgets fail CI; latency does not.

The engine builds with `CGO_ENABLED=0`. It is not dependency-free: it uses about a dozen Go modules,
all of them pure Go, for SQLite, PostgreSQL, ACME, YAML, minification, QR codes, images, XML, log
rotation and screenshots.

## The repository

- `compiler`, `runtime`, `value`, `builtins`, `conformance`, `compatibility`: the language, from
  text to verified bytecode to the machine, and the corpora that freeze them.
- `core`, `app`, `site`, `request`, `work`: the host, the ownership hierarchy, routes, lifecycle,
  caches, schedules, queues and events.
- `render`, `ui`, `jit`: templates, the `.ui` compiler and its stylesheet, the CSS, icon, logo, font,
  highlight, kernel and component generators.
- `kitdb`, `database`, `search`, `utilities`, `capabilities`: data, connections, the search engine,
  the HTTP client and the helpers, and the capability lifetimes.
- `domain`, `gateway`, `proxies`: ACME, the reverse proxy, the forward proxies.
- `cmd`: `releasegate`, `canary`, `vmcompat` and the KitDB tools. The root files of the module hold
  `Run`, `Check`, `Profile`, `InspectFile` and the manifest reader.

## Read next

- `docs/RUNTIME_ARCHITECTURE.md`: who owns what, from the host down to a request.
- `docs/STABILITY.md`: the invariants and the commands that prove them.
- `docs/BYTECODE.md` and `docs/VM.md`: the instruction format, the verifier, the machine.
- `docs/APP.md`: every manifest method.
- `docs/PUBLISHING.md`: RSS, sitemaps and typed responses.
- `docs/RELEASE.md` and `docs/DIAGNOSTICS.md`: how a release is qualified and observed.
- `kitdb/README.md`, `kitdb/RELEASE_1_0.md`, `kitdb/VISION.md`: the database, the exact claim it
  makes, and where it is going.
- `CONTRIBUTING.md`: the rules that catch people, and why.

The Kitwork repository holds the `.ui` specification (`docs/KITWORK_UI.md`), the type definitions
for editors (`kitwork.d.ts`), the task-specific guides under `.claude/skills/`, an example site
written in `.ui` (`docs/examples/kitwork`), and the author's sites. The ideas behind the engine,
thirty of them in reading order, are published at huynhnhanquoc.com/concepts.

## License

The engine is licensed under the GNU Affero General Public License, version 3 (`LICENSE`), with
a commercial licence offered alongside for organisations that cannot ship AGPL software.
`LICENSE-EXCEPTION.md` states the intended rule that an application the engine runs is a separate
work, and `CLA.md` is the contributor agreement; both are drafts and not yet in force. The editor
type definitions in the Kitwork repository are MIT, so that what you copy into your own code stays
permissive.

Security issues go to support@kitwork.org, not to a public issue. A tenant escape, a way past the
energy ceiling, or a path out of a site's own directory is the class that matters most.

Kitwork is written by Huỳnh Nhân Quốc (`FOUNDER.md`).
