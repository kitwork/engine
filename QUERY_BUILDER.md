# 🗄️ Kitwork Industrial Query Builder
> **"Turning Complex SQL into Elegant, Type-Safe Logic."**

The Kitwork Query Builder is a high-performance database SDK designed for industrial-grade applications. It collapses traditional multi-line SQL boilerplate into descriptive, deterministic JavaScript statements executed by the Kitwork VM at nanosecond speeds.

---

## 📚 Table of Contents
1. [🔍 Selection Primitives](#-selection-primitives)
2. [🎯 Advanced Filtering (The Power of Lambdas)](#-advanced-filtering)
3. [📊 Sorting & Pagination](#-sorting--pagination)
4. [🔗 Joins & Aggregates](#-joins--aggregates)
5. [✍️ Data Mutators (CRUD)](#️-data-mutators-crud)
6. [🛠️ Real-World Use Cases](#️-real-world-use-cases)

---

## 🔍 Selection Primitives
Kitwork offers explicit methods to define exactly *how* you want to retrieve data.

| Method | Return Type | Use Case |
| :--- | :--- | :--- |
| `.list()` | `Array` | Fetch all records matching the criteria. |
| `.take(n)` | `Array` | Fetch exactly `n` records. |
| `.first()` | `Object \| Null` | Get the first matching record. |
| `.last()` | `Object \| Null` | Get the most recent record (by ID DESC). |
| `.find(id)` | `Object \| Null` | High-speed lookup by Primary Key. |
| `.exists()` | `Boolean` | Check if any record matches the criteria. |

---

## 🎯 Advanced Filtering
Stop writing error-prone strings. Use **Magic Lambda Syntax** for safe column interaction.

### 1. Simple Comparison
```javascript
db.products.where(p => p.price > 500).list();
```

### 2. Automatic Set Inclusion (Auto-IN)
Simply pass an array to the comparison operator, and Kitwork translates it to a `SQL IN` clause.
```javascript
// SQL: SELECT * FROM users WHERE id IN (1, 5, 10);
const users = db.user.where(u => u.id == [1, 5, 10]).list();
```

### 3. Pattern Matching (Auto-LIKE)
Include a `%` wildcard in your string, and the engine automatically switches to `LIKE`.
```javascript
// SQL: SELECT * FROM products WHERE name LIKE 'Apple%';
const fruits = db.products.where(p => p.name == "Apple%").list();
```

### 4. Multiple Conditions
Chained `.where()` calls are treated as an `AND` operation.
```javascript
const activeAdmins = db.user
    .where(u => u.role == "admin")
    .where(u => u.is_active == true)
    .list();
```

---

## 📊 Sorting & Pagination
Kitwork provides clean primitives for handling large datasets.

```javascript
const page2 = db.orders
    .sort(o => o.created_at, "desc") // Sort by date descending
    .skip(50)                       // Offset by 50
    .take(25);                      // Limit to 25
```

---

## 🔗 Joins & Aggregates

### Smart Relationships
```javascript
// Variable 'profiles' is automatically reflected to the "profiles" table
const data = db.users
    .join(profiles => profiles.user_id == users.id)
    .list();
```

### Data Analytics
| Method | Description |
| :--- | :--- |
| `.count()` | Returns the total number of records. |
| `.sum(col)` | Returns the sum of a specific column. |
| `.avg(col)` | Returns the average value. |
| `.max(col)` | Returns the maximum value. |

```javascript
const totalRevenue = db.orders.where(o => o.status == "paid").sum("amount");
```

---

## ✍️ Data Mutators (CRUD)
Kitwork mutators are **Strict & Returning** – they return the full updated object from the database.

```javascript
// 1. CREATE
const user = db.user.create({ 
    email: "new@kitwork.io", 
    role: "member" 
});

// 2. UPDATE (Strict Mode: Requires .where())
const updated = db.user
    .where(u => u.id == user.id)
    .update({ login_count: iconcrement(1) });

// 3. SOFT DELETE (Sets deleted_at)
db.user.where(u => u.id == 1).delete();

// 4. HARD DESTROY (Physical removal)
db.user.where(u => u.id == 1).destroy();
```

---

## 🛠️ Real-World Use Cases

### Case A: Fetching Top 5 Expensive Products
```javascript
const premiumItems = db.products
    .sort(p => p.price, "desc")
    .take(5);
```

### Case B: Checking User Permission (Existence Check)
```javascript
const canAccess = db.permissions.exists(p => p.user_id == uid && p.scope == "admin");
if (!canAccess) status(403);
```

### Case C: Complex Activity Feed
```javascript
const feed = db.activity
    .where(a => a.user_id == [1, 2, 3]) // IN check
    .where(a => a.type != "internal")   // Inequality
    .sort(a => a.id, "desc")            // Recent first
    .take(10);
```

### Case D: Quick Statistics
```javascript
const stats = {
    totalUsers: db.users.count(),
    pendingOrders: db.orders.where(o => o.status == "pending").count(),
    avgOrderValue: db.orders.avg("total_price")
};
```

---
*Kitwork Engine - Industrial Logic infrastructure.*
