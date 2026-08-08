# Kitwork Engine — Developer Experience (DX) Backlog

This backlog prioritizes all developer experience enhancements, documentation fixes, CLI tooling improvements, and onboarding refinements.

---

## 1. DX Backlog Master Table

| Task ID | Task Title | Category | Priority | Target Release | Effort | Description |
|---|---|---|---|---|---|---|
| **DX-01** | **Reconcile Routing Docs** | Documentation | **Blocker** | v1.0.0-RC1 | Low | Update `ARCHITECTURE.md` to describe `router.kitwork.js` and `page.kitwork.html` tree routing. |
| **DX-02** | **Publish `kitwork-starter` Template** | Templates | **Blocker** | v1.0.0-RC1 | Medium | Provide clean 1-click starter repository for new developers. |
| **DX-03** | **Implement `kitwork dev` CLI Wrapper** | CLI / Tooling | **High Impact** | v1.0.0-RC1 | Medium | Provide single CLI command to boot local dev server with `ALLOW_LOCAL=true` and hot reload. |
| **DX-04** | **Enhance `kitwork check` Error Snippets** | Diagnostics | **High Impact** | v1.0.0-RC1 | Medium | Display 3-line code context snippet around syntax and parse errors in `kitwork check` CLI output. |
| **DX-05** | **Publish JS Subset Cheatsheet** | Documentation | **High Impact** | v1.0.0-RC1 | Low | Create `docs/LANGUAGE_SPEC.md` documenting arrow functions, banned syntax (`while`), and array comma rules. |
| **DX-06** | **Implement `kitwork init <domain>`** | CLI / Tooling | **Nice to Have** | v1.1.0 | Medium | Add CLI command to generate boilerplate application structure automatically. |
| **DX-07** | **Add Production Dockerfile Template** | Deployment | **Nice to Have** | v1.1.0 | Low | Provide multi-stage build `Dockerfile` and `docker-compose.yml` for 1-command server deployment. |
| **DX-08** | **Interactive Web Playground** | Web Tooling | **Future** | v1.2.0 | High | Provide online VM playground disassembling bytecode and executing Kitwork JS scripts in browser. |

---

## 2. Priority Focus for v1.0.0-RC1

The top 3 DX items (**DX-01**, **DX-02**, **DX-03**) ensure a brand new developer can discover Kitwork, clone a working starter template, understand the filesystem routing rules, and boot a local development server in **under 60 seconds** without reading engine Go code.
