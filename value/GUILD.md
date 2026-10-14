# Kitwork Value: The Atom of Logic

> **"One Type to Rule Them All."**

**Kitwork Value** is the fundamental atomic unit of data in the Engine.
It is a **Dynamic Type System** engineered to run safely within the strict, statically-typed environment of Go.

Every variable, every database record, every JSON response, and every calculation result in Kitwork is a `Value`.

---

## ⚛️ The Philosophy

In standard Go, handling mixed data types (JSON `any`, SQL `NULL`, Map `interface{}`) is painful. Type assertions (`.(string)`) panic if you are wrong. Nil pointers crash your server.

**Kitwork Value** eliminates this fear.
*   **Never Panic**: Operations on mismatched types fail gracefully (e.g., `1 + "a"` might be handled or produce safe zero).
*   **Nil is Safe**: Accessing a property of a `Nil` value simply returns another `Nil` value, not a crash.
*   **Fluid Conversion**: It creates a bridge between the rigid Go world and the flexible Logic world.

---

## 🛠 Internal Architecture

A `Value` is a lightweight struct that carries both its Data and its Identity (Kind).

```go
type Value struct {
    N float64     // Optimized number cache (fast-path for math)
    V any         // The Raw Data (Maps, Arrays, Strings, Objects)
    K Kind        // The Identity Discriminator
    S Sub         // Sub-type / Metadata information
}
```

### The Kind System (`kind.go`)
We support a rich set of high-level types:
*   **Primitives**: `Nil`, `Bool`, `Number`, `String`, `Time`, `Duration`
*   **Complex**: `Array`, `Map`, `Struct`
*   **Logic**: `Func` (Script), `Error`
*   **Special**: `Proxy` (for DB magic), `Bytes`

---

## ⚡ Core Capabilities

### 1. Universal Constructor
Convert *anything* into a Kitwork Value instantly.

```go
v1 := value.New("Hello")
v2 := value.New(123)
v3 := value.New(map[string]any{"id": 1})
```

### 2. Deep Navigation (`navigation.go`)
Access deeply nested data structure without fear.

```go
// Code: user.address.city
// If 'address' is nil, this returns Nil Value (safe), does not crash.
city := user.Get("address").Get("city")
```

### 3. Safe Arithmetic (`arithmetic.go`)
Perform math on unknown types safely.

```go
// Auto-converts compatible types
// 10 + "20" -> 30 (Engine tries to be helpful)
sum := v1.Add(v2)
```

### 4. Smart SQL Proxy (`sql.go`)
The `Value` system knows how to talk to databases.
If a Value is a `Proxy`, operations on it doesn't compute result, but build a Query.

```javascript
// JS World
db.users.where(u => u.id == 1) 
// The 'u' here is a Value(Kind: Proxy). 
// 'u.id' returns a new Proxy recording the path "id".
// '== 1' returns a Proxy recording the Condition.
```

### 5. Rich Standard Library (`methods.go`)
Every value comes with built-in powerful methods:

*   **String**: `.upper()`, `.lower()`, `.trim()`, `.includes()`, `.split()`, `.replace()`
*   **Array**: `.push()`, `.pop()`, `.join()`, `.reverse()`, `.shuffle()`, `.unique()`, `.compact()`
*   **Map**: `.keys()`, `.has()`, `.merge()`, `.delete()`
*   **Global**: `.len()`, `.json()`, `.int()`, `.float()`

A map's own key wins over every one of these on a read, as an own property shadows a prototype method in JavaScript: `{ delete: 3 }.delete` and `o["delete"]` are `3`, `{ text: "hi" }.text` is `"hi"`. A call reaches the own key only when it holds a function (`CallsOwn`): `{ has: f }.has(x)` calls `f`, but an own key holding data never answers a call to a builtin method, so `body.pick("name")` is the builtin whatever keys a request body brings (`Invoke` and the VM's `INVOKE`). A name that is neither a function the value owns, nor a builtin of its kind, nor a Go method of a host value (`hasGoMethod`; a Go method without arguments is a getter that `Get` has already called) is not a function: since 14/10/2026 `Invoke` gives the `NotAFunction` failure (code `NotAFunctionCode`, `"NOT_A_FUNCTION"`; message `x không phải hàm (là số): không gọi được`, which names the kind only, never a string's text or a number), where it gave the value read (`{ x: 3 }.x()` was `3`). The VM puts the object in front (`o.x …`, `runtime/callee.go`) and the place in the failure's `at` (`router.kitwork.js:12:5`), which a printed diagnostic leads with; a template puts the path it wrote (`item.n …`). A method of `Nil` is still `Nil`, and a failure passes on as it is. `Invoke` calls an own host function but cannot run a VM lambda (`Call` gives `Invalid`), so a template prints `invalid` for a lambda member. A map without that key keeps the method (13/10/2026; `Get`, `HasOwn`, `CallsOwn`). On a plain map an own `error`, `length` or `isError` is data too (`{ ok: false, error: "x" }.error` is `"x"`), always: since 14/10/2026 no value carries an error attached to its data, so no object keeps the engine's `.error`, `.isError` and `.length` over its own.

---

## 🛡 The Standard of Stability

The Logic Engine relies entirely on this package. It is the bedrock.
Because `Value` handles all the dirty work of type checking and conversion, the VM instructions (`ADD`, `GET`, `CALL`) can be extremely simple and fast.

**Kitwork Value** makes Go feel as dynamic as JavaScript, but as robust as steel.

---
*© Kitwork Project - The Atomic Layer*
