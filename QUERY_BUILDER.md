# 🚀 Kitwork Engine - Ultra-Smart Query Builder

Kitwork Engine provides a powerful, minimalist, and elite database query SDK (Query Builder), allowing you to write complex SQL statements using pure JavaScript syntax.

## 🌟 Key Feature: "The Power of ONE"

Kitwork's philosophy is minimalism. We advocate for a "Primary-First" approach where you use the most descriptive methods for clear code.

### 1. Magic Lambda Syntax
Instead of writing error-prone strings, Kitwork uses arrow functions (Lambdas) to interact with columns.

```javascript
// Minimalist, safe, and supports code autocompletion
db().table("user").where(u => u.username == "boss").take();
```

### 2. Elite Naming Standard
We provide descriptive methods that explicitly state how you want to "take" the results:

*   **`.take(n?)`**: The most powerful way to execute a query. 
    *   `take()`: Returns an array of all matches.
    *   `take(5)`: Returns an array of the first 5 matches.
*   **`.one()` / `.first()`**: Explicitly returns a **Single Object** (or null).
*   **`.last()`**: Returns the latest entry (ordered by ID or existing order).
*   **`.all()`**: Alias for `.take()`.

---

## 🛠 Usage Guide

### Fetching Data
| Feature | Syntax | Expected SQL |
| :--- | :--- | :--- |
| Find by ID | `.find(1)` | `WHERE "id" = 1` |
| Get One Entity | **`.one()`** or **`.first()`** | `LIMIT 1` |
| Get Last Entity | **`.last()`** | `ORDER BY id DESC LIMIT 1` |
| Get Collection | **`.take(n?)`** | `SELECT *` |

### Smart Operator Detection
Kitwork automatically "translates" JavaScript code to SQL based on data values:

*   **Auto-LIKE**: `db().table("user").where(u => u.username == "Apple%").take();`
*   **Auto-IN**: `db().table("user").where(u => u.id == [10, 20]).take();`

### Filters
```javascript
db().table("products")
    .where(p => p.price > 1000)
    .where(p => p.stock <= 5)
    .take(3); // Take top 3 low stock products
```

---

## 🔒 Security & Performance

*   **SQL Injection-Proof**: Kitwork uses *Prepared Statements* ($1, $2, ...) for every input value.
*   **Reflection-Powered**: The Engine uses Go Reflection to extract JavaScript data accurately.
*   **Zero-Overhead**: Compiled directly into query structures at the Go layer.

---
*Kitwork Engine - Simple is the new Smart.*
