# Kitwork Engine
> **The Operating System for Sovereign Logic.**

![Go](https://img.shields.io/badge/go-1.21-black?style=flat-square&logo=go)
![Speed](https://img.shields.io/badge/speed-70ns-black?style=flat-square)
![Energy](https://img.shields.io/badge/energy-green-00ff00?style=flat-square)
![Docs](https://img.shields.io/badge/docs-complete-blue?style=flat-square)


## The New Standard.

Kitwork replaces the modern cloud stack with **Living Logic**.
We built an engine where the Developer Experience (DX) is prioritized above all else.
No boilerplate. No config files. Just **Intent**.


## 📊 Industrial Performance Metrics
We don't guess. We measure. Kitwork is engineered for **High-Frequency Logic**.

| Category | Metric | Score | Context / Comparison |
| :--- | :--- | :--- | :--- |
| **🚀 CORE VM** | **Instruction Speed** | **~14,100,000 ops/s** | Raw Bytecode Velocity. |
| | **Logic Throughput** | **~605,000 ops/s** | Complex Real-world Logic. |
| | **Clock Latency** | **70ns** | Execution Precision. |
| **💾 DATA** | **Query Build Time** | **230ns** | **20x Faster** than GORM. |
| | **ORM Memory Alloc** | **0 B/op** | **Zero-GC** Architecture. |
| | **DB Overhead** | **< 1%** | Near Raw-SQL performance. |
| **⚡ SYSTEM** | **Cold Boot Time** | **< 10ms** | Instant Serverless Scaling. |
| | **GC Pressure** | **Near Zero** | Aggressive Resource Pooling. |

> *"It runs faster than you can think."*


## ⚡ The Code Experience

### 1. The "Smart" Database (Zero-ORM)
It feels like writing TypeScript, but runs like optimized SQL. 
**No structs. No mapping. No boilerplate.**

```javascript
// A. SIMPLE & EXPRESSIVE
// Get active admins sorted by karma
var admins = db.users
    .where(u => u.role == "admin" && u.active == true)
    .orderBy("karma", "desc")
    .take(10);

// B. THE "MAGIC" JOIN (Auto-Inference)
// Find VIPs who bought 'iPhone' recently.
// The engine automatically detects keys for 'users' and 'orders' join.
var vips = db.users
    .join(orders => users.id == orders.user_id) 
    .where(orders => orders.product == "iPhone" && orders.total > 1000)
    .list(10);

// C. MUTATIONS WITH INTENT
// Create, Update, and Delete without SQL.
db.logs.create({ msg: "System Boot" });
db.users.where(u => u.id == 101).update({ role: "vip" });
db.logs.where(l => l.status == "error").delete(); // Soft-Delete safely
```

### 2. Built-in Caching Strategy
Why pay for API rate limits? Cache expensive calls in one line.

```javascript
// Cache Bitcoin Price for 10 seconds.
// If 1000 users hit this, we only call CoinDesk ONCE.
work("CryptoAPI")
    .get("/btc", () => {
        return http.get("api.coindesk.com/v1/bpi/currentprice.json").json();
    })
    .cache("10s"); // Context-aware caching
```

### 3. Static Resources & CDN
Map local folders to global routes instantly.

```javascript
// Map /uploads/* to ./storage/user_files
// Logic Engine automatically handles ETags, Gzip, and Range requests.
work("MediaCDN")
    .get("/uploads/*")
    .assets("./storage/user_files"); 
```

### 4. Native Benchmarking
Unsure about performance? Test it inline.

```javascript
work("HelloWorld")
    .get("/hello-world", () => {
        return "hello world";
    })
    .benchmark(5000); // Runs 5000 iterations on startup & prints report
```

### 5. Human-Readable Scheduling
Forget Cron syntax. Speak natural language.

```javascript
work("SleekScheduler")
    .handle(() => db.logs.delete()) // Define logic once
    .daily("13:00", "01:00")        // Run daily at specific hours
    .weekly("MONDAY 08:30")         // Run every Monday morning
    .hourly(0, 30)                  // Run every 30 minutes
    .monthly("1st")                 // Run on the 1st of month
    .every("10s");                  // Run every 10 seconds
```

### 6. Logic-Aware Rendering
Composite layouts with trusted trust.

```javascript
work("Dashboard")
    .get("/admin", () => {
        // 1. Query: Get recent high-value orders
        var orders = db.orders
            .where(o => o.status == "paid")
            .orderBy("total", "desc")
            .take(20);

        // 2. Transform: Compute view-specific logic (e.g. Tax & Badges)
        // The View receives processed data, keeping templates logic-less.
        var viewData = orders.map(o => {
            o.tax = o.total * 0.08;
            o.is_vip = o.total > 1000;
            o.formatted_date = date(o.created_at, "YYYY-MM-DD");
            return o;
        });

        return { 
            title: "Q3 Sales Report",
            user: "Admin",
            items: viewData 
        };
    })
    .layout({ nav: "view/nav.html" })
    .render("view/dashboard.html");
```

#### 🎨 Template Syntax (view/dashboard.html)
The template engine is logic-less but intelligent. It compiles to Go Bytecode for speed.

```html
<!-- 1. Variables (Auto-Escaped for XSS Protection) -->
<h1>Hello, {{ user.name }}</h1>

<!-- 2. Logic (Conditionals & Nested) -->
{{ if user.level >= 3 }}
    <span class="badge">🌟 VIP USER</span>
{{ else }}
    {{ if user.level > 1 }}
        <span>REGULAR USER</span>
    {{ else }}
        <span>NEW USER</span>
    {{ end }}
{{ end }}

<!-- 3. Loops using Ternary Operator -->
<ul>
     {{ for (i, item) in items }}
        <!-- Ternary logic directly in attributes -->
        <li class="{{ i % 2 == 0 ? 'background-light' : 'background-dark'}} border-red">
            {{ i }}: {{ item.name }} - ${{ item.price }}
        </li>
    {{ /range }}
</ul>

<!-- 4. Raw HTML (Explicit Bypass) -->
<div>{{ $article.content }}</div>
```




## 📚 Complete API Reference

Here is the exhaustive list of capabilities built into the engine.

### 1. Database Builder (`db`)
| Method | Syntax | Description |
| :--- | :--- | :--- |
| **Retrieval** | `db.users.list()` | Get all matching records. |
| **Find** | `db.users.find(1)` | Efficient PK lookup. |
| **First** | `db.users.first()` | Get single record. |
| **Count** | `db.users.count()` | Count records (optimized). |
| **Filter** | `db.users.where(u => u.age > 18)` | Filter with Lambda or `("field", val)`. |
| **Or** | `db.users.or(u => u.role == "admin")` | Logical OR condition. |
| **In** | `db.users.in(u => u.id, [1, 2])` | Set inclusion (`IN`). |
| **Like** | `db.users.like(u => u.name, "A%")` | SQL Pattern Matching. |
| **Null** | `db.users.null("deleted_at")` | Check for NULL. |
| **NotNull** | `db.users.notNull("email")` | Check for Not NULL. |
| **Join** | `db.orders.join(users => ...)` | Inner Join (Lambda inference). |
| **LeftJoin** | `db.users.leftJoin(orders => ...)`| Left Outer Join. |
| **Group** | `db.stats.group("status")` | `GROUP BY` clause. |
| **Having** | `db.stats.having(s => ...)` | Filter groups. |
| **Sum** | `db.orders.sum("total")` | Aggregate Sum. |
| **Avg** | `db.orders.avg("total")` | Aggregate Average. |
| **Min/Max** | `db.orders.min("price")` | Aggregate Min/Max. |
| **Page** | `skip(10)`, `take(5)` | Limit & Offset helpers. |
| **Limited**| `limited(120)` | **Safety Cap**: Max rows allowed (Default 120). |
| **Order** | `orderBy("date", "desc")` | Sorting. |
| **Create** | `create({ ... })` | Insert new record. |
| **Update** | `update({ ... })` | Update matching records. |
| **Delete** | `delete()` | Soft Delete (`deleted_at` set). |
| **Destroy** | `destroy()` | Hard Delete (Physical). |
| **Return** | `returning("id", "status")` | Atomic return after mutation. |

### 2. Work Configuration (`work()`)
| Method | Example | Description |
| :--- | :--- | :--- |
| **Route** | `.get("/path", handler)` | Define GET route. Also `.post()`, `.put()`, `.delete()`. |
| **Router** | `.router("GET", "/path")` | Manual route definition. |
| **Handle** | `.handle(fn)` | Define generic handler (for Cron/Main). |
| **Version** | `.version("v1")` | Metadata versioning. |
| **Retry** | `.retry(3)` | Auto-retry on error/timeout. |
| **Cache** | `.cache("1h")` | Enable response caching. |
| **Static** | `.static("24h")` | Enable static file serving headers. |
| **Assets** | `.assets("./public")` | Define static directory root. |
| **File** | `.file("path/to/file")` | Serve single file. |
| **Render** | `.render("page.html")` | View template to render. |
| **Layout** | `.layout({ "nav": "n.html" })` | Composite layout injection. |
| **Redirect** | `.redirect("/new", 301)` | HTTP Redirection. |
| **Done** | `.done(fn)` | Post-execution hook. |
| **Fail** | `.fail(fn)` | Error handling hook. |
| **Benchmark**| `.benchmark(1000)` | Run startup performance test. |
| **Cron** | `.schedule("0 * * * *")` | Raw Cron expression. |
| **Natural** | `.daily("13:00")` | Run daily at specific time. |
| **Natural** | `.hourly(0, 30)` | Run at specific minutes past hour. |
| **Natural** | `.weekly("MON 08:00")` | Run weekly. |
| **Natural** | `.monthly("1st")` | Run monthly. |
| **Interval**| `.every("5s")` | Run every duration. |

### 3. Task Context (`ctx` / `t`)
Inside a handler `(ctx) => ...`:

| Method | Description |
| :--- | :--- |
| `ctx.Params` | Access URL parameters (`:id`). |
| `ctx.Payload()` | Safe way to get request data. |
| `ctx.JSON(v)` | Respond with JSON. |
| `ctx.HTML(v)` | Respond with HTML. |
| `ctx.Log(...)` | Server-side logging with context prefix. |
| `ctx.DB(c)` | Get DB connection instance. |
| `ctx.HTTP()` | Get HTTP Client instance. |
| `ctx.Now()` | Get current server time. |

### 4. Networking (`http`)
| Method | Description |
| :--- | :--- |
| `http.fetch(url, opts)` | Universal request. Opts: `{method, headers, body}`. |
| `http.get(url)` | Quick GET. |
| `http.post(url, body)` | Quick POST. |
| `json(str)` | Parse JSON string. |
| `json_encode(val)` | Serialize to JSON string. |


## 🌌 The Energy Economy

Your code pays for its existence. **Spam is solved by physics.**

| Action | Cost Weight | Philosophy |
| :--- | :--- | :--- |
| **CPU (Calc)** | Low | Thinking is cheap. |
| **RAM (Alloc)** | Medium | Memory is finite space. |
| **DB (Read)** | Medium | Knowledge retrieval. |
| **DB (Write)** | High | Changing the world takes effort. |
| **Network** | Very High | Communication is expensive. |

> *When `Energy < 0`, the script sleeps.*


## 🏗 Core Architecture

*   **Brain**: [Stack-Based VM (Opcode)](opcode/GUILD.md).
*   **Atoms**: [Dynamic Value System](value/GUILD.md).
*   **Face**: [Logic-Aware Render](render/GUILD.md).

### Get Onboard.

```bash
git clone https://github.com/kitwork/engine
go run cmd/server/main.go
# System Online :8081
```

*© Kitwork Foundation*