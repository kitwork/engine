# Kitwork Standard Library (STDLIB) Specification

This document details the built-in functions, context methods, and value prototypes available in the Kitwork script environment.

## 1. Global Context Functions

These functions are available everywhere. Many are shortcuts (aliases) to the current `work` context.

### `work(options)`
Creates or defines the current logic organism.
- **options**: String (name) or Object `{ name: "..." }`.
- **Returns**: `Work` context object.

### `db()`
Initializes a fluent database query builder.
- **Returns**: `DBQuery` object.

### `now()`
Returns the current system time.
- **Returns**: `Time` value.

### `json(data)`
Alias for `work.json(data)`. Sets the primary response as JSON and returns the `work` context for chaining.

---

## 2. Work Context (`w.`)

The `work` object (often assigned to `const w`) holds both metadata and runtime behavior.

### Directives (Discovery Phase)
These methods are used by the engine to "learn" about the script's infrastructure.
- **`.router(method, path)`**: Registers an HTTP endpoint.
- **`.retry(count, interval)`**: Defines a retry policy.
- **`.version(semver)`**: Tags the script version.

### Response Handlers (Execution Phase)
- **`.json(value)`**: Encapsulates a value into a JSON response.
- **`.text(value)`**: Returns raw text.
- **`.html(value)`**: Returns HTML content.

---

## 3. Database Builder (`db().`)

### `.from(tableName)`
Sets the target table.

### `.take(n)` or `.limit(n)`
Limits the number of results.

### `.get()`
Executes the query and returns an **Array of Objects**.
*Note: If a script ends with a DB builder object without calling `.get()`, the engine automatically executes it.*

---

## 4. Value Prototypes (Chaining)

Every value in Kitwork (numbers, strings, booleans, results) supports method chaining for fast transformations.

### Casting
- **`.string()`**: Force convert value to string.
- **`.int()`**: Force convert value to integer.
- **`.float()`**: Force convert value to floating point.

### Formatting
- **`.json()`**: Wraps the value as the final JSON response (useful at the end of a chain).
- **`.text()`**: Converts value to its most readable text representation.

---

## 5. Examples

### Advanced Chaining
```javascript
let count = db().from("logs").take(100).get().len();
return count.string().json(); // "100" as JSON
```

### Full Context Usage
```javascript
work("OrderProcess")
  .router("POST", "/process")
  .retry(5, "5s");

let order = payload.json();
print("Processing:", order.id);

return db().from("inventory").take(1); // Auto-executes .get() and returns JSON
```
