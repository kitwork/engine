# 🚀 Kitwork Engine - Ultra-Smart Query Builder

Kitwork Engine provides a powerful, minimalist, and elite database query SDK (Query Builder), allowing you to write complex SQL statements using pure JavaScript syntax.

## 🌟 Key Feature: "The Power of ONE"

Kitwork's philosophy is minimalism. You only need to use the `.where()` function for almost all query needs. The Engine automatically infers the appropriate SQL operator based on the data you provide.

### 1. Magic Lambda Syntax
Instead of writing error-prone strings, Kitwork uses arrow functions (Lambdas) to interact with columns in the database.

```javascript
// Minimalist, safe, and supports code autocompletion
db().table("user").where(u => u.username == "boss").get();
```

### 2. Smart Operator Detection
Kitwork Engine automatically "translates" JavaScript code to SQL based on data values:

*   **Auto-LIKE Detection**: Triggered when a string value contains the `%` character.
    ```javascript
    // Translated to: WHERE "username" LIKE 'Apple%'
    db().table("user").where(u => u.username == "Apple%").get();
    ```
*   **Auto-IN Detection**: Triggered when the value is an Array.
    ```javascript
    // Translated to: WHERE "id" IN (10, 20, 30)
    db().table("user").where(u => u.id == [10, 20, 30]).get();
    ```

---

## 🛠 Usage Guide

### Basic Queries
| Feature | Syntax | Expected SQL |
| :--- | :--- | :--- |
| Find by ID | `.find(1)` | `WHERE "id" = 1` |
| Get 1 Record | `.first()` | `LIMIT 1` |
| Ordering | `.orderBy("age", "DESC")` | `ORDER BY "age" DESC` |
| Pagination | `.limit(10).offset(20)` | `LIMIT 10 OFFSET 20` |

### Filters
In addition to the smart `==` operator, Kitwork supports a full range of comparisons:

```javascript
db().table("products")
    .where(p => p.price > 1000)
    .where(p => p.stock <= 5)
    .where(p => p.status != "hidden")
    .get();
```

### Aggregates
Support for database-level calculations:
```javascript
let stats = {
    total: db().table("orders").sum("amount"),
    average: db().table("products").avg("price"),
    max_score: db().table("players").max("score")
};
```

---

## 🔒 Security & Performance

*   **SQL Injection-Proof**: Kitwork uses *Prepared Statements* ($1, $2, ...) for every input value. Your data is always isolated from the execution command.
*   **Reflection-Powered**: The Engine uses Reflection at the Go layer to extract JavaScript data accurately, ensuring absolute stability.
*   **Zero-Overhead**: Lambda syntax is compiled directly into query structures at the Go layer, causing near-zero latency for the VM.

---

## 🚀 Get Started Now

Define your API in the `demo/api` directory and enjoy a modern development experience:

```javascript
work("UserAPI")
    .get("/api/users", () => {
        const minAge = query("age") || 18;
        
        return db().table("user")
            .where(u => u.age >= minAge)
            .orderBy("age", "ASC")
            .get();
    });
```

---
*Kitwork Engine - Simple is the new Smart.*
