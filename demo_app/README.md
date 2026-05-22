# 🚀 Kitwork VM Showcase Portal (Demo App)

This is a complete, self-contained showcase portal illustrating the capabilities of the **Kitwork Engine**. It runs on a stack-based virtual machine in Go by parsing and executing `app.kitwork.js` inside the `tenants` source directory.

---

## 🏗️ Folder Structure

```
demo_app/
├── config.kitwork.yaml       # Configuration defining port (8080) and source folder (tenants)
└── tenants/
    └── test/
        └── localhost/         # Domain-mapped tenant workspace
            ├── app.kitwork.js # JS Controller containing routes, caches, and routines
            ├── assets/
            │   └── logo.png   # Futuristic AI-generated brand asset logo
            └── views/         # Directory containing layouts and partial templates
                ├── index.kitwork.html      # Shell layout wrapper
                ├── _head_.kitwork.html     # SEO, Google Fonts & Tailwind CDN config
                ├── _navbar_.kitwork.html   # Glassmorphic header
                ├── _footer_.kitwork.html   # Footer metadata
                ├── page.kitwork.html       # Hero Landing Page
                ├── dashboard/
                │   └── page.kitwork.html   # Telemetry cards and API interactive tester
                ├── about/
                │   └── page.kitwork.html   # Manifesto / documentation content
                └── notfound.kitwork.html   # 404 page template
```

---

## ⚡ Key Features Exhibited

1. **Routing System**: Dynamic URL matching and route groups (`/`, `/dashboard`, `/about`, `/api/*`, `/*`).
2. **Template Assembly & Bind**: The compiler automatically constructs the template hierarchy starting from `index.kitwork.html` and binding local + global variables dynamically.
3. **HTTP Fetch Proxy**: The `/api/quote` endpoint calls out to an external mock quote provider via a Go HTTP client.
4. **LRU Request Caching**: The `/api/quote` endpoint is cached for `10s` to prevent heavy redundant requests.
5. **Background Goroutines**: Spawned naturally inside Javascript via the `go()` directive.

---

## 🏁 How to Run

1. Open your terminal at the repository root.
2. Stop any existing server tasks running on port `8080`.
3. Navigate to the `demo_app` folder:
   ```bash
   cd demo_app
   ```
4. Start the engine from the parent directory:
   ```bash
   go run ../cmd/server/main.go
   ```
5. Open your browser and navigate to:
   👉 **[http://localhost:8080/](http://localhost:8080/)**

Explore the **Dashboard** page and click **Execute Query** on the different cards to test real-time VM operations and watch the JSON outputs.
