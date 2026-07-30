# Kitwork Engine — Application Exception

> **DRAFT — not yet in force.** This text has not been reviewed by a lawyer. Until it is, and until
> it is published alongside a release, no additional permission has been granted. Review it before
> the first external contributor or the first commercial licence.

Additional permission under GNU AGPL version 3, section 7.

Kitwork Engine is licensed under the GNU Affero General Public License, version 3 (see `LICENSE`).
The following additional permission is granted alongside it.

## Why this exists

Kitwork exists to run other people's applications. You drop a folder under `apps/` and the engine
compiles and serves it. Without an explicit statement, a careful reader has to ask whether their
application becomes a derivative work of the engine — and a question a lawyer cannot answer quickly
is, in practice, a refusal.

The answer intended by the authors is no: an application is input to an interpreter, in the same way
a shell script is input to a shell. This exception says so in writing rather than leaving it to be
argued.

## Definitions

**"Engine"** — the Kitwork Engine software distributed under the AGPL, and any work derived from it.

**"Application"** — files that the Engine reads, compiles, renders or executes as input, and that are
not themselves derived from Engine source code. This includes `.kitwork.js` modules, `.kitwork.html`
templates, static assets, collection content, per-application configuration and data.

**"Generated Output"** — artifacts the Engine produces from an Application, at build time or while
serving a request. This includes compiled bytecode, rendered HTML, JIT-generated CSS, icons, logos
and fonts, feed and sitemap documents, and any client runtime the Engine serves to an end user's
browser.

## Grant

1. **An Application is a separate and independent work.** Compiling, executing or serving an
   Application with the Engine does not place that Application under the AGPL. You may license,
   distribute and keep private your Application under any terms you choose.

2. **Generated Output is yours.** You may use, distribute and serve Generated Output under any terms
   you choose, including output the Engine serves into an end user's browser. Receiving a page from
   a Kitwork site does not place that page, or the site, under the AGPL.

3. **Running the Engine unmodified is not a trigger.** Operating an unmodified Engine as a network
   service, whether for yourself or for others, creates no obligation to publish your Applications
   or your data.

## What this exception does NOT do

4. **It does not cover the Engine itself.** If you modify Engine source code and make the modified
   Engine available to users over a network, AGPL section 13 applies to those modifications in full.
   This is the obligation the licence exists to create, and this exception does not weaken it.

5. **It grants no trademark rights.** "Kitwork" and the Kitwork marks are not licensed here. A
   modified Engine must not be presented as Kitwork.

6. **It is not a patent grant beyond the AGPL's own.**

## Removal

As permitted by AGPL section 7, a downstream recipient may remove this additional permission from
their copy. Doing so removes it only from that copy.

## Separately licensed components

Some parts of the Kitwork project are deliberately NOT under the AGPL, because they are meant to be
copied into your own codebase:

| Component | Licence |
| :--- | :--- |
| `@kitwork/kitjs` — the client kernel, as an npm package | MIT |
| `kitwork.d.ts` — type definitions for editors | MIT |

The rule the project follows: **what you copy into your own code is permissive; what you run as a
service is AGPL.**

---

Copyright (c) 2026 Huỳnh Nhân Quốc. Questions about commercial licensing: support@kitwork.org
