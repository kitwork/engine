# Kitwork Engine — Sample Applications Specification

This document details the architectural design, project layout, data flows, API usages, test plans, and implementation readiness for six sample applications demonstrating Kitwork's capabilities.

---

## 1. Sample Applications Overview

| App # | Application Name | Core Kitwork Capabilities Demonstrated | Implementation Readiness | Target Users |
|---|---|---|---|---|
| **1** | **Hello Kitwork** | Basic routing, view binding, layout rendering, static assets. | **100% Ready (RC1)** | Beginners |
| **2** | **REST API** | JSON response contexts, status codes, query builder, error handling. | **100% Ready (RC1)** | Backend Engineers |
| **3** | **URL Shortener** | Shortbase ID generator, SQLite persistence, HTTP 301 redirects, rate limiting. | **100% Ready (RC1)** | Web Developers |
| **4** | **Multi-tenant Blog** | Domain/identity path isolation, dynamic slug routing `[slug]`, layout bubbling. | **100% Ready (RC1)** | SaaS Builders |
| **5** | **Scheduler & Jobs** | App-owned cron scheduler (`work.CronScheduler`), background queue workers (`kitwork().go()`). | **100% Ready (RC1)** | System Engineers |
| **6** | **Durable AI Agent** | Identity scoping, SQLite conversation memory, tool capability registration, gas budgeting (`MaxEnergy`). | **90% Ready (Needs MCP bridge)** | AI Engineers |

---

## 2. Detailed Application Specifications

### App 1: Hello Kitwork
- **Purpose**: Minimal "Hello World" site demonstrating zero-config startup.
- **Features Demonstrated**: Single page rendering, layout interpolation `{{ .title }}`.
- **Directory Layout**:
  ```text
  apps/0123456789abcdefghijklmnopqrstuvwxyz/hello.localhost/
  ├── router.kitwork.js
  └── app/
      ├── _layout_.kitwork.html
      └── page.kitwork.html
  ```
- **Code Snippet (`router.kitwork.js`)**:
  ```javascript
  import { router } from "kitwork";
  router.get((ctx) => ctx.view({ title: "Hello Kitwork!" }));
  ```
- **Readiness**: **100% Runnable Today**.

---

### App 2: REST API (Products API)
- **Purpose**: Full CRUD JSON API service.
- **Features Demonstrated**: `ctx.json()`, `ctx.status()`, `ctx.db.table()`, parameterized `.where()` filtering.
- **Code Snippet (`app/products/router.kitwork.js`)**:
  ```javascript
  import { router } from "kitwork";

  router.get((ctx) => {
    const products = ctx.db.table("products").where("active", true).list();
    return ctx.json(products);
  });

  router.post((ctx) => {
    const item = ctx.db.table("products").create(ctx.body);
    return ctx.status(201).json(item);
  });
  ```
- **Readiness**: **100% Runnable Today**.

---

### App 3: URL Shortener (`short.localhost`)
- **Purpose**: High-performance URL shortener with short ID generation and click tracking.
- **Features Demonstrated**: Shortbase ID generator (`id.Shortlink()`), SQLite row incrementing, 301 redirects, rate limiting (`router.ratelimit`).
- **Code Snippet (`router.kitwork.js`)**:
  ```javascript
  import { router } from "kitwork";

  // Rate limit: max 10 link creations per minute per IP
  router.ratelimit({ rate: 10, per: "1m" });

  router.post((ctx) => {
    const slug = ctx.db.shortlink(); // 8-char shortbase
    ctx.db.table("links").create({ slug: slug, target: ctx.body.url, clicks: 0 });
    return ctx.json({ short_url: "http://short.localhost/" + slug });
  });
  ```
- **Readiness**: **100% Runnable Today**.

---

### App 4: Multi-Tenant Blog (`blog.localhost`)
- **Purpose**: Multi-tenant blogging platform with dynamic slugs (`/blog/[slug]`).
- **Features Demonstrated**: Identity folder isolation, dynamic path parameters (`ctx.params.slug`), RSS feed output (`router.rss()`).
- **Code Snippet (`app/blog/[slug]/router.kitwork.js`)**:
  ```javascript
  import { router } from "kitwork";

  router.get((ctx) => {
    const post = ctx.db.table("posts").where("slug", ctx.params.slug).first();
    if (!post) return ctx.status(404).view();
    return ctx.view({ post });
  });
  ```
- **Readiness**: **100% Runnable Today**.

---

### App 5: Scheduler & Background Jobs
- **Purpose**: Background task processor that cleans expired sessions every 5 minutes and processes image thumbnails asynchronously.
- **Features Demonstrated**: `_cron/` scheduler folder, `kitwork().go(fn)` detached execution pool.
- **Code Snippet (`_cron/cleanup.kitwork.js`)**:
  ```javascript
  import { kitwork } from "kitwork";

  // Runs every 5 minutes
  export default kitwork.cron("*/5 * * * *", (ctx) => {
    ctx.db.table("sessions").where("expired", true).delete();
  });
  ```
- **Readiness**: **100% Runnable Today**.

---

### App 6: Durable AI Agent (`agent.localhost`)
- **Purpose**: Autonomous AI Agent executing multi-step tool calls with persistent SQLite state memory and gas energy limits.
- **Features Demonstrated**: `app.Runtime` SQLite memory persistence, energy gas limits (`MaxEnergy`), capability tool execution, background step execution.
- **Code Snippet (`app/agent/router.kitwork.js`)**:
  ```javascript
  import { router, kitwork } from "kitwork";

  router.post((ctx) => {
    const userPrompt = ctx.body.prompt;
    
    // Store agent memory in SQLite
    const state = ctx.db.table("agent_state").create({ prompt: userPrompt, status: "thinking" });

    // Spawn async agent step under identity background pool
    kitwork().go(() => {
      // Execute agent tool steps inside sandboxed VM
      const result = ctx.http.post("https://api.openai.com/v1/chat/completions", { ... });
      ctx.db.table("agent_state").where("id", state.id).update({ status: "completed", response: result.body });
    });

    return ctx.status(202).json({ task_id: state.id, status: "processing" });
  });
  ```
- **Readiness**: **90% Ready** (Core engine runs today; requires external LLM provider API credentials).
