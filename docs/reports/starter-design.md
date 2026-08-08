# Kitwork Engine — Starter Project Specification (`kitwork-starter`)

This document defines the official design, directory structure, sample code, and extension guide for the minimal production-ready `kitwork-starter` template repository.

---

## 1. Starter Project Directory Layout

```text
apps/0123456789abcdefghijklmnopqrstuvwxyz/myapp.localhost/
├── .env                           # Local environment variables
├── config.kitwork.yaml             # Domain & database configuration
├── router.kitwork.js              # Site root router & context injection
├── app/                           # Filesystem route tree
│   ├── _layout_.kitwork.html      # Root HTML layout wrapper
│   ├── page.kitwork.html          # Homepage HTML template ("/")
│   ├── api/
│   │   └── router.kitwork.js      # JSON REST API routes ("/api/v1")
│   └── notes/
│       ├── page.kitwork.html      # Notes list view ("/notes")
│       └── router.kitwork.js      # Notes CRUD handlers
└── public/                        # Static assets (Zero-VM direct streaming)
    ├── favicon.ico
    └── assets/
        └── app.css
```

---

## 2. Sample Code Implementation

### 2.1 Site Root Router (`router.kitwork.js`)
```javascript
import { router } from "kitwork";

// Intercept root requests and attach common site metadata
router.meta({ title: "My Kitwork Application" });

// Root route handler
router.get((ctx) => {
  return ctx.view({
    message: "Welcome to Kitwork Engine!",
    time: new Date().toISOString()
  });
});
```

### 2.2 Root HTML Layout (`app/_layout_.kitwork.html`)
```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>{{ $.meta.title }}</title>
  <link rel="stylesheet" href="/assets/app.css">
  <script data-kit-jit="theme"></script>
</head>
<body class="bg-slate-900 text-slate-100 p-8">
  <header class="mb-8 border-b border-slate-700 pb-4">
    <h1 class="text-2xl font-bold">Kitwork Starter</h1>
  </header>

  <main>
    {{ slot }}
  </main>
</body>
</html>
```

### 2.3 Homepage View (`app/page.kitwork.html`)
```html
<section class="max-w-xl mx-auto space-y-4">
  <h2 class="text-xl font-semibold text-sky-400">{{ .message }}</h2>
  <p class="text-slate-400">Server time: <span class="font-mono">{{ .time }}</span></p>
  <a href="/notes" class="inline-block bg-sky-600 hover:bg-sky-500 text-white px-4 py-2 rounded">
    View Notes App &rarr;
  </a>
</section>
```

### 2.4 Notes REST Handlers (`app/notes/router.kitwork.js`)
```javascript
import { router } from "kitwork";

// GET /notes — list notes from tenant SQLite DB
router.get((ctx) => {
  const notes = ctx.db.table("notes").list();
  return ctx.view({ notes });
});

// POST /notes — create new note
router.post((ctx) => {
  const title = ctx.body.title;
  if (!title) {
    return ctx.status(400).json({ error: "Title is required" });
  }
  const created = ctx.db.table("notes").create({
    title: title,
    created_at: new Date().toISOString()
  });
  return ctx.status(201).json(created);
});
```

---

## 3. How to Run and Extend

### Running Locally
```bash
# 1. Clone starter inside engine root or apps/
git clone https://github.com/kitwork/kitwork-starter apps/0123456789abcdefghijklmnopqrstuvwxyz/myapp.localhost

# 2. Boot host with local development mode enabled
ALLOW_LOCAL=true PORT=8080 go run .

# 3. Open browser at http://localhost:8080
```

### Validation Check
```bash
# Run preflight verifier
go run . check
```
