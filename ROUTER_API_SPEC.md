# ROUTER_API_SPEC

Version: Draft 1

This document defines the behavior of `router.kitwork.js`.

A router is the entry point of every runtime node.

Every request entering a folder MUST execute its corresponding
`router.kitwork.js` before continuing to child folders.

A router does not define URL paths.

The filesystem defines the URL.

The router defines runtime behavior.

---

# Router Lifecycle

Request

↓

Enter Folder

↓

Execute router.kitwork.js

↓

Execute Folder Pipeline

↓

Continue Traversal

↓

Resolve HTTP Method

↓

Execute Method Pipeline

↓

Handler

---

# Router Responsibilities

A router is responsible for

• Access Control

• Middleware

• Request Context

• Metadata

• Rate Limiting

• Cache

• Error Handling

• HTTP Methods

A router is NOT responsible for

• URL Mapping

• Route Prefixes

• URL Parameters

Those are provided by the filesystem.

---

# Folder Router

A folder router represents a runtime node.

Example

/users/router.kitwork.js

The runtime executes this router every time a request enters

/users

regardless of the final destination.

---

Available APIs

router.guard()

router.middleware()

router.limit()

router.cache()

router.meta()

router.error()

router.get()

router.post()

router.put()

router.patch()

router.delete()

router.options()

router.head()

---

# Folder Guard

Runs before entering child folders.

Example

router.guard((request, response) => {

    if (!request.user)

        return response.redirect("/login")

})

Execution Order

Root

↓

Parent

↓

Current

If any guard stops the request

Traversal immediately stops.

---

# Folder Middleware

Runs after guard.

May

Modify Request

Modify Response

Load Resources

Inject Context

Example

router.middleware((request) => {

    request.user = loadUser()

})

---

# Folder Limit

Rate limiting.

Example

router.limit({
    type: "ip" (default) | "brower" | "user"
    rate: 100,
    per: "1s" | "1m" | "1month" | "1week"
    scope: "router" (default) | "tenant" | "server"
})

---

# Folder Cache

Defines cache policy.

Example

router.cache({

    ttl: "5m"

})

---

# Folder Meta

Attach metadata.

Example

router.meta({

    layout: "dashboard"

})

Metadata automatically flows to child folders.

Child routers may override parent metadata.

---

# Folder Error

Handles runtime exceptions.

Example

router.error((error, request, response) => {

    response.view()

})

Error Resolution

Current Router

↓

Parent Router

↓

Root Router

Nearest handler wins.

---

# HTTP Methods

Each HTTP Method is an independent runtime node.

Example

router.get()

router.post()

router.put()

router.patch()

router.delete()

Each method has its own execution pipeline.

---

# Method Pipeline

Available APIs

.guard()

.middleware()

.limit()

.cache()

.meta()

.error()

.handle()

Example

router.get()

    .guard(...)

    .middleware(...)

    .limit(...)

    .cache(...)

    .meta(...)

    .error(...)

    .handle(...)

---

# Method Guard

Runs after all folder guards.

Execution Order

Folder Guards

↓

Method Guard

↓

Handler

---

# Method Middleware

Runs before handler.

May modify

Request

Response

Context

---

# Method Limit

Overrides folder limit.

Nearest definition wins.

---

# Method Cache

Overrides folder cache.

Nearest definition wins.

---

# Method Meta

Overrides folder metadata.

Nearest definition wins.

---

# Method Error

Handles only this method.

Resolution

Method Error

↓

Folder Error

↓

Parent Folder Error

↓

Root Error

---

# Handler

A method MUST have exactly one handler.

Example

router.get()

    .handle((request, response) => {

    })

The handler generates the final response.

---

# Request Context

Every router receives the same Request Context.

The context is shared during traversal.

Root Router

↓

Users Router

↓

User Router

↓

Posts Router

↓

Method

↓

Handler

Every router may extend the context.

---

# Traversal Order

Folders execute

Outside

↓

Inside

Methods execute

After traversal finishes.

Example

Request

/users/quoc/posts/123

Execution

Root Router

↓

Users Router

↓

User Router

↓

Posts Router

↓

Post Router

↓

GET

↓

Handler

---

# Dynamic Parameters

Parameters are resolved by folders.

Examples

{id}

{id[number]}

{slug}

{slug(regex)}

Resolved values become

request.params

Example

request.params.id

request.params.slug

---

# Folder Priority

Resolution Order

Exact Folder

↓

Dynamic Folder

↓

Not Found

Example

users/

↓

profile/

↓

{user}/

↓

notfound

Exact folders always win.

---

# NotFound

The router may optionally define

router.notFound()

Example

router.notFound((request, response) => {

})

Resolution

Current

↓

Parent

↓

Root

If no router handler exists

↓

Render

notfound.kitwork.html

---

# Response Types

A handler may return

response.view()

response.json()

response.text()

response.file()

response.stream()

response.redirect()

response.download()

response.empty()

---

# Runtime Rules

A router is compiled only once.

Compiled routers remain cached.

Routers are executed for every request.

Folders are traversed one by one.

Traversal never skips intermediate folders.

Methods execute only after traversal completes.

Pages are local.

Layouts are inherited.

Slots are inherited.

Errors bubble upward.

NotFound bubbles upward.

The filesystem defines the application.

The router defines the runtime.

---

# Design Philosophy

Router First

Filesystem First

Runtime First

Lazy Compilation

Hierarchical Execution

Outside → Inside Traversal

Inside → Outside View Resolution

Everything begins with a router.