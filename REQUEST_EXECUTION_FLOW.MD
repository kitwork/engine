# KITWORK REQUEST EXECUTION FLOW

This document defines the complete lifecycle of an HTTP request inside Kitwork.

Unlike traditional frameworks, Kitwork does not build a routing table during startup.

Instead, every request traverses the filesystem at runtime.

The filesystem itself is the runtime.

---

# Overview

Request

↓

Resolve Domain

↓

Traverse Filesystem

↓

Execute Routers

↓

Resolve Method

↓

Execute Handler

↓

Resolve View

↓

Render Response

Every step is executed lazily.

Nothing is scanned during application startup.

---

# 1. Engine Boot

Application starts.

Initialize only the runtime environment.

Initialize

• HTTP Server

• JavaScript Runtime

• Cache Manager

• Tenant Manager

• Logger

Do NOT

• Scan project directories

• Parse HTML files

• Compile router.kitwork.js

• Build routing tables

• Discover pages

• Resolve layouts

The engine is idle until the first request arrives.

---

# 2. Incoming Request

Example

GET https://example.com/users/quoc/posts/123

↓

Split Request

Domain

example.com

Method

GET

Segments

users

quoc

posts

123

The engine does not resolve the final path immediately.

---

# 3. Domain Resolution

Locate the tenant directory.

example.com/

↓

Exists?

YES

↓

Continue

NO

↓

Return 404

The root directory must contain

router.kitwork.js

If the router does not exist

↓

Return 404

The root router is always the first execution point.

---

# 4. Filesystem Traversal

Traversal is sequential.

Outside

↓

Inside

Example

example.com/

↓

users/

↓

{user}/

↓

posts/

↓

{id[number]}/

The runtime walks one folder at a time.

It never jumps directly to the destination.

---

# 5. Folder Resolution

For every segment

The runtime tries

Exact Folder

↓

Dynamic Folder

↓

No Match

Example

Segment

quoc

Try

quoc/

↓

Exists?

NO

↓

Search

{user}/

↓

Matched

↓

Bind

user = "quoc"

Continue

Every parameter is resolved independently.

---

# 6. Router Resolution

Whenever a folder is entered

The runtime checks

router.kitwork.js

Compiled?

NO

↓

Compile

↓

Cache

↓

Execute

YES

↓

Execute Cached Runtime

Each router may define

• guard()

• middleware()

• limit()

• cache()

• error()

• get()

• post()

• put()

• patch()

• delete()

Every router executes before entering the next folder.

---

# 7. Guard Pipeline

Folder guards execute from

Outside

↓

Inside

Example

Root Guard

↓

Users Guard

↓

User Guard

↓

Posts Guard

↓

Post Guard

If any guard rejects

↓

Stop traversal

↓

Return response

No remaining routers are executed.

---

# 8. Method Resolution

After traversal reaches the final folder

Resolve HTTP Method

GET

↓

POST

↓

PUT

↓

PATCH

↓

DELETE

The router selects the matching method.

If the method does not exist

↓

Start NotFound Resolution

---

# 9. Method Execution

Each method is an independent runtime node.

Execution order

guard()

↓

middleware()

↓

limit()

↓

cache()

↓

meta()

↓

handle()

The handler generates the response.

---

# 10. View Resolution

If the handler returns

response.view()

The runtime begins view resolution.

Current Folder

↓

page.kitwork.html

Exists?

YES

↓

Continue

NO

↓

Start NotFound Resolution

Page is never inherited.

Only the current folder is searched.

---

# 11. Index Resolution

The runtime resolves the nearest layout.

Current Folder

↓

Parent

↓

Parent

↓

Root

Nearest

index.kitwork.html

wins.

Example

<html>

{{ @head }}

<body>

{{ @header }}

{{ @page }}

{{ @footer }}

</body>

</html>

The index defines page composition.

---

# 12. Slot Resolution

Every slot follows the same algorithm.

Current Folder

↓

Parent

↓

Parent

↓

Root

Nearest file wins.

Example

{{ @head }}

↓

head.kitwork.html

Example

{{ @header }}

↓

header.kitwork.html

Example

{{ @footer }}

↓

footer.kitwork.html

Example

{{ @sidebar }}

↓

sidebar.kitwork.html

Example

{{ @navbar }}

↓

navbar.kitwork.html

Slots are automatically resolved.

The runtime never hardcodes slot names.

---

# 13. NotFound Resolution

When

• Folder does not exist

• Method does not exist

• page.kitwork.html does not exist

The runtime searches

Current Folder

↓

Parent

↓

Parent

↓

Root

Nearest

notfound.kitwork.html

wins.

If none exists

↓

Return Default 404

---

# 14. Error Resolution

If an exception occurs

Method Error

↓

Current Router Error

↓

Parent Router Error

↓

Parent Router Error

↓

Root Router Error

Nearest error handler wins.

If none exists

↓

Return Default 500

---

# 15. Response

The runtime sends

HTML

JSON

Stream

SSE

File

Redirect

depending on the handler result.

---

# 16. Lazy Compilation

router.kitwork.js

is compiled only when it is first requested.

Compiled routers remain cached.

Subsequent requests reuse the compiled runtime.

HTML templates may also be cached independently.

---

# 17. Complete Execution Timeline

Engine Boot

↓

Incoming Request

↓

Resolve Domain

↓

Traverse Filesystem

↓

Resolve Folder

↓

Execute Router

↓

Execute Folder Guard

↓

Continue Traversal

↓

Resolve HTTP Method

↓

Execute Method Guard

↓

Execute Middleware

↓

Execute Limit

↓

Execute Cache

↓

Execute Handler

↓

Resolve Page

↓

Resolve Index

↓

Resolve Slots

↓

Generate HTML

↓

Send Response

---

# Runtime Philosophy

Everything begins with a router.

Every folder is a runtime node.

Every request traverses the filesystem.

Routers execute from outside to inside.

Methods execute after traversal completes.

Pages are local.

Layouts are inherited.

Slots are inherited.

Errors bubble upward.

NotFound bubbles upward.

The filesystem is the runtime.