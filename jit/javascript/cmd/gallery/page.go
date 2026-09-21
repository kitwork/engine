package main

// galleryHTML is one standalone document that mounts the sealed bundle served
// at /kit.js. Every interactive control is authored HTML + data-kit-* directives;
// the components own only reactive state (and, for copy/otp, their own listeners).
const galleryHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>KitJS — Component Gallery</title>
<style>
  :root {
    --bg: #fafafa; --panel: #ffffff; --ink: #16161a; --muted: #6b6b76;
    --line: #e6e6ea; --accent: #f82244; --accent-soft: #fdecef; --ok: #12855a;
    --mono: ui-monospace, "SF Mono", "JetBrains Mono", "Cascadia Code", Menlo, Consolas, monospace;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; background: var(--bg); color: var(--ink); font-family: var(--mono);
    line-height: 1.5; -webkit-font-smoothing: antialiased;
  }
  header {
    padding: 32px 24px 20px; border-bottom: 1px solid var(--line); background: var(--panel);
  }
  header h1 { margin: 0; font-size: 20px; letter-spacing: -0.02em; }
  header h1 b { color: var(--accent); }
  header p { margin: 6px 0 0; color: var(--muted); font-size: 13px; }
  header code { background: var(--accent-soft); color: var(--accent); padding: 1px 6px; border-radius: 4px; font-size: 12px; }
  main {
    max-width: 1080px; margin: 0 auto; padding: 24px;
    display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 18px;
  }
  .card {
    background: var(--panel); border: 1px solid var(--line); border-radius: 12px;
    padding: 18px; display: flex; flex-direction: column; gap: 14px;
  }
  .card h2 { margin: 0; font-size: 14px; }
  .card h2 span { color: var(--muted); font-weight: 400; font-size: 11px; margin-left: 6px; }
  .card .desc { margin: -6px 0 0; color: var(--muted); font-size: 12px; }
  .body { display: flex; flex-direction: column; gap: 10px; }
  .row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
  button {
    font-family: var(--mono); font-size: 13px; cursor: pointer;
    border: 1px solid var(--line); background: #fff; color: var(--ink);
    border-radius: 8px; padding: 7px 12px; transition: border-color .12s, background .12s;
  }
  button:hover { border-color: var(--accent); }
  button:disabled { opacity: .4; cursor: not-allowed; border-color: var(--line); }
  button.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
  input {
    font-family: var(--mono); font-size: 13px; padding: 7px 10px;
    border: 1px solid var(--line); border-radius: 8px; background: #fff; color: var(--ink); width: 100%;
  }
  input:focus, button:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
  output, .value { font-variant-numeric: tabular-nums; }
  .big { font-size: 22px; min-width: 2ch; text-align: center; }
  .muted { color: var(--muted); font-size: 12px; }
  .track { height: 8px; border-radius: 999px; background: var(--line); overflow: hidden; }
  .fill { height: 100%; background: var(--accent); border-radius: 999px; }
  .stars { display: flex; gap: 4px; }
  .stars button { border: none; background: none; font-size: 24px; padding: 0 2px; color: var(--accent); line-height: 1; }
  .chips { display: flex; flex-wrap: wrap; gap: 6px; min-height: 28px; }
  .chip { display: inline-flex; align-items: center; gap: 6px; background: var(--accent-soft); color: var(--accent); border-radius: 999px; padding: 3px 6px 3px 10px; font-size: 12px; }
  .chip button { border: none; background: none; color: var(--accent); padding: 0 2px; font-size: 14px; line-height: 1; }
  .panel { border: 1px dashed var(--line); border-radius: 8px; padding: 12px; color: var(--muted); font-size: 12px; }
  .menu { border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
  .menu .option { display: block; width: 100%; text-align: left; border: none; border-radius: 0; border-bottom: 1px solid var(--line); }
  .menu .option:hover { background: var(--accent-soft); }
  .otp { display: flex; gap: 8px; }
  .otp input { width: 44px; height: 52px; text-align: center; font-size: 22px; padding: 0; }
  code.snippet { background: #16161a; color: #f5f5f7; border-radius: 8px; padding: 8px 10px; font-size: 12px; display: block; overflow-x: auto; }
  footer { text-align: center; color: var(--muted); font-size: 11px; padding: 20px; }
  footer code { color: var(--accent); }
</style>
</head>
<body>
  <header>
    <h1><b>KitJS</b> — Component Gallery</h1>
    <p>Eight common components, sealed into one standalone bundle by the engine composer and served at <code>/kit.js</code>. Every control below is plain HTML + <code>data-kit-*</code>.</p>
  </header>

  <main>
    <!-- stepper -->
    <section class="card" data-kit-component="stepper@1.0.0" data-kit-scope="value: 3, min: 0, max: 10, step: 1">
      <h2>stepper <span>state-only</span></h2>
      <p class="desc">Bounded numeric value with guarded +/−.</p>
      <div class="body">
        <div class="row">
          <button data-kit-click="decrement()" data-kit-bind:disabled="!canDecrement()">−</button>
          <output class="big" data-kit-text="value">3</output>
          <button data-kit-click="increment()" data-kit-bind:disabled="!canIncrement()">+</button>
          <span class="muted">range 0–10</span>
        </div>
      </div>
    </section>

    <!-- slider -->
    <section class="card" data-kit-component="slider@1.0.0" data-kit-scope="value: 40, min: 0, max: 100, step: 5, page: 20">
      <h2>slider <span>state-only</span></h2>
      <p class="desc">Step, page, and a live <code>percent()</code> track fill.</p>
      <div class="body">
        <div class="track"><div class="fill" data-kit-style="width: percent() + '%';"></div></div>
        <div class="row">
          <button data-kit-click="pageDown()">−20</button>
          <button data-kit-click="decrement()">−5</button>
          <output class="big" data-kit-text="value">40</output>
          <button data-kit-click="increment()">+5</button>
          <button data-kit-click="pageUp()">+20</button>
        </div>
      </div>
    </section>

    <!-- rating -->
    <section class="card" data-kit-component="rating@1.0.0" data-kit-scope="value: 0, max: 5">
      <h2>rating <span>state-only</span></h2>
      <p class="desc">Click a star; <code>isFilled(n)</code> projects the fill.</p>
      <div class="body">
        <div class="stars">
          <button data-kit-click="rate(1)" data-kit-text="isFilled(1) ? '★' : '☆'">☆</button>
          <button data-kit-click="rate(2)" data-kit-text="isFilled(2) ? '★' : '☆'">☆</button>
          <button data-kit-click="rate(3)" data-kit-text="isFilled(3) ? '★' : '☆'">☆</button>
          <button data-kit-click="rate(4)" data-kit-text="isFilled(4) ? '★' : '☆'">☆</button>
          <button data-kit-click="rate(5)" data-kit-text="isFilled(5) ? '★' : '☆'">☆</button>
        </div>
        <div class="row">
          <output data-kit-text="value + ' / 5'">0 / 5</output>
          <button data-kit-click="clear()">Clear</button>
        </div>
      </div>
    </section>

    <!-- tags -->
    <section class="card" data-kit-component="tags@1.0.0" data-kit-scope="tags: ['kitwork', 'go'], draft: '', max: 6">
      <h2>tags <span>state-only</span></h2>
      <p class="desc">Enter to add, × to remove, deduped, max 6.</p>
      <div class="body">
        <div class="chips">
          <template data-kit-for="tag of tags">
            <span class="chip"><span data-kit-text="tag"></span><button data-kit-click="remove(tag)" aria-label="remove">×</button></span>
          </template>
        </div>
        <div class="row">
          <input data-kit-model="draft" data-kit-keydown:enter="add(draft)" placeholder="Add a tag, press Enter">
          <button class="primary" data-kit-click="add(draft)" data-kit-bind:disabled="!canAdd()">Add</button>
        </div>
        <span class="muted"><output data-kit-text="tags.length">2</output> / 6</span>
      </div>
    </section>

    <!-- collapse -->
    <section class="card" data-kit-component="collapse@1.0.0">
      <h2>collapse <span>state-only</span></h2>
      <p class="desc">Single guarded disclosure.</p>
      <div class="body">
        <button data-kit-click="toggle()" data-kit-bind:aria-expanded="open" data-kit-text="open ? 'Hide details ▲' : 'Show details ▼'">Show details ▼</button>
        <div class="panel" data-kit-show="open" hidden>
          The panel toggles the <code>hidden</code> attribute — it stays in the DOM, so authored CSS owns any height transition.
        </div>
      </div>
    </section>

    <!-- combobox -->
    <section class="card" data-kit-component="combobox@1.0.0" data-kit-click:outside="hide()" data-kit-scope="options: ['Apple', 'Apricot', 'Banana', 'Blackberry', 'Cherry', 'Dragonfruit', 'Elderberry', 'Fig', 'Grape', 'Kiwi'], query: '', open: false, activeIndex: -1, selected: ''">
      <h2>combobox <span>state-only</span></h2>
      <p class="desc">Type to filter <code>options</code>; click to choose.</p>
      <div class="body">
        <input data-kit-model="query" data-kit-input="search()" data-kit-focusin="show()" data-kit-keydown:escape="hide()" placeholder="Type to filter fruit…">
        <div class="menu" data-kit-show="open" hidden>
          <template data-kit-for="option of filtered()">
            <button class="option" data-kit-click="choose(option)" data-kit-text="option"></button>
          </template>
        </div>
        <span class="muted"><output data-kit-text="filtered().length">10</output> match · <output data-kit-text="selected ? 'chose ' + selected : 'nothing chosen'">nothing chosen</output></span>
      </div>
    </section>

    <!-- copy -->
    <section class="card" data-kit-component="copy@1.0.0" data-kit-scope="text: 'go run -C engine ./jit/javascript/cmd/gallery', delay: 1500">
      <h2>copy <span>init · requires clipboard</span></h2>
      <p class="desc">Writes via the sealed clipboard service; flag self-resets.</p>
      <div class="body">
        <code class="snippet">go run -C engine ./jit/javascript/cmd/gallery</code>
        <button class="primary" data-kit-click="copy()" data-kit-text="copied ? '✓ Copied to clipboard' : 'Copy command'">Copy command</button>
      </div>
    </section>

    <!-- otp -->
    <section class="card" data-kit-component="otp@1.0.0">
      <h2>otp <span>init</span></h2>
      <p class="desc">Digits auto-advance; paste a code; Backspace steps back.</p>
      <div class="body">
        <div class="otp">
          <input data-otp-slot inputmode="numeric" autocomplete="one-time-code" aria-label="digit 1">
          <input data-otp-slot inputmode="numeric" aria-label="digit 2">
          <input data-otp-slot inputmode="numeric" aria-label="digit 3">
          <input data-otp-slot inputmode="numeric" aria-label="digit 4">
        </div>
        <div class="row">
          <button data-kit-click="clear()">Clear</button>
          <output data-kit-text="isComplete() ? 'complete: ' + value : (value || '— — — —')">— — — —</output>
        </div>
      </div>
    </section>
  </main>

  <footer>
    sealed bundle <code>sha256:__BUNDLE_HASH__</code> · zero-eval closed runtime · one document, no CDN
  </footer>

  <script src="/kit.js"></script>
</body>
</html>`
