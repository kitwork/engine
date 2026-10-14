# Kitwork Language Conformance Corpus

This corpus freezes observable language behavior independently from parser,
compiler, and runtime unit tests. Sources live in `testdata/`; `corpus.json`
declares whether each source must execute, return a structured diagnostic, or
be rejected during compilation.

Every accepted source crosses this boundary:

```text
source file
  -> native compiler
  -> bytecode artifact encode/decode/re-encode
  -> verifier-owned immutable Program
  -> fresh VM
  -> previously-used VM
  -> released and reacquired app VM pool lease
```

The three executions must publish the same variables, result or diagnostic,
instruction count, energy, and final stack/frame state. Add a fixture whenever
language semantics change. Keep host capabilities out of this corpus; their
contracts belong to end-to-end route tests. The only globals a fixture sees are
the language's own two, `safe(() => …)` and `fail("…")` (14/10/2026), which every
host gives a program.

Run it directly with:

```text
go test ./conformance -run TestLanguageConformanceCorpus -count=1
```

## Kitwork UI

`testdata/ui/<fixture>/cases.json` holds Kitwork UI cases, language-neutral too
(sources, JSON, HTML):

- a case with a `route` and an `expect` file renders the fixture's `site/` with
  the case's `scope` and compares the HTML (`quancaphe` is a coffee shop written
  in `.ui`, the fixture alone since its example left `docs/examples` on
  13/10/2026; its `assets/` give the `<img>` its `width` and `height`). Every class of every
  branch (the page's skeleton) must have its rule in the page's own stylesheet,
  or be a word `reboot.css` styles, an icon, a logo, or the language of a code
  element (`language-go`, HTML's own way to name it, which nothing styles); no
  class may be a word of Tailwind's dialect. `theme` is the site's
  `router.colors`, over Kit Design's default palette;
- a case with its own `files` (paths from the site root, `../.components/…` for
  the identity) and an `error` must not compile: the error points at
  `error.at` (`file:line:col`) and its message holds `error.says`. `forms`
  holds the errors an author meets with the three forms of markup: a tag, a
  layout called by its tag (a region, or a piece with only a `.ui`:
  `<product-card />`) and `component=` for a piece with logic. A case with its
  own `files` and an `expect` renders them. `code` holds `<code language="…">`
  coloured on the server: one `<span class="code-<kind>">` per token that is not
  plain text, every text escaped, a language from `{{ }}` the engine does not
  have shown plain, and a language it does not have written in the page an
  error that says what to write;
- every page compiles with exactly the `warnings` its case lists (none when it
  lists none), each at `at` and holding `says`: a pair of colours short of
  contrast is a warning by default, and the compile error when the case says
  `"contrast": "strict"` (`router.contrast`).

```text
go test ./conformance -run TestUIConformance -count=1
```
