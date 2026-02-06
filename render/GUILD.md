# Kitwork Render Engine

> **"Logic-less but Powerful. Native Speed. Nanosecond Rendering."**

Kitwork Render is a high-performance, zero-dependency Template Engine written in pure Go. It strikes a perfect balance between simplicity (keeping clean views) and power (allowing necessary presentation logic).

Designed for the Kitwork Ecosystem, it supports dynamic expression evaluation, scoping, and advanced control structures directly within HTML.

---

## � Key Features

*   **Native Performance**: Direct parsing and compilation. No reflection overhead where possible.
*   **Expression Power**: Supports Arithmetic (`+`, `-`, `*`, `/`, `%`), Comparisons (`==`, `!=`, `>`, `<`), and Logic (`&&`, `||`).
*   **Null Safety**: Built-in **Null Coalescing Operator (`??`)** to handle missing data gracefully.
*   **Smart Scoping**: Automatically flattens root context into loops, making top-level variables (like `users.length`) accessible inside nested blocks.
*   **Ternary Operator**: Concise conditional rendering `{{ cond ? "active" : "inactive" }}`.

---

## 📚 Syntax Reference

### 1. Variable Output
Variables are automatically HTML-escaped to prevent XSS.

```html
<!-- Basic user info -->
<h1>{{ user.name }}</h1>

<!-- Access nested properties -->
<p>Email: {{ user.contact.email }}</p>

<!-- Null Coalescing: Fallback if value is null/empty -->
<p>Bio: {{ user.bio ?? "No bio provided" }}</p>

<!-- Raw Output (Unescaped) - Use with caution! -->
<div>{{ raw(user.html_content) }}</div>
```

### 2. Control Structures: Conditionals (`if`)

The engine supports robust conditional logic, including arithmetic checks within conditions.

#### Basic Check
```html
{{ if user.is_active }}
    <span class="status green">Active</span>
{{ else }}
    <span class="status red">Inactive</span>
{{ end }}
```

#### Comparison Logic
```html
<!-- Compare numbers or strings -->
{{ if user.role == "admin" }}
    <button>Delete System</button>
{{ end }}

<!-- Complex Logic with Parentheses -->
{{ if (user.age >= 18) && (user.verified == true) }}
    <p>Welcome to the adult section.</p>
{{ end }}
```

#### Modulo / Arithmetic Check
Commonly used for styling alternating rows.
```html
{{ if i % 2 == 0 }}
    <div class="row-even">...</div>
{{ else }}
    <div class="row-odd">...</div>
{{ end }}
```

### 3. Iteration (`for`)

Iterate over Arrays or Maps with ease. The engine supports tuple unpacking for index/key and value.

```html
<ul>
    <!-- 'users' is an array -->
    {{ for (i, user) in users }}
        <li>
            <!-- Logic inside loop -->
            Index: {{ i + 1 }} - Name: {{ user.name }}
            
            <!-- Access Root Scope from inside loop -->
            (Total: {{ users.length }} users)
        </li>
    {{ end }}
</ul>
```

### 4. Inline Assignments (`let`)
Define local variables within the template scope to simplify complex logic.

```html
{{ let totalPrice = item.price * item.quantity }}
<p>Total: {{ totalPrice }}</p>

{{ if totalPrice > 100 }}
    <b>High Value Order!</b>
{{ end }}
```

### 5. Advanced Expressions

Calculate presentation values directly in the view without polluting the backend controller.

*   **Ternary Operator**:
    ```html
    <tr class="{{ i == (users.length - 1) ? 'border-none' : 'border-b' }}">
    ```

*   **Arithmetic**:
    ```html
    <div style="width: {{ percent * 100 }}%;"></div>
    <span>Next Page: {{ current_page + 1 }}</span>
    ```

*   **Logical OR (Fallback)**:
    ```html
    <!-- If title is falsy, use 'Untitled' -->
    <h1>{{ post.title || "Untitled" }}</h1>
    ```

---

## 💡 Design Philosophy

1.  **Strict Separation of Concerns**: Heavy business logic (DB queries, complex algorithms) belongs in the Script (JS/Go). The View handles **Presentation Logic** only (formatting, status colors, conditional display).
2.  **Developer Experience (DX)**: We believe a template engine should be helpful, not restrictive. We provide operators like `+` and `? :` because forcing developers to write helper functions just to increment an index (`i+1`) is counter-productive.
3.  **Safety First**: Output is escaped by default. Logic is sandboxed within the scope.

---
*© Kitwork Template - Logic Infrastructure*
