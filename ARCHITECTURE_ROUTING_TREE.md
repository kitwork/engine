# Kitwork Routing & View Architecture Tree

This document defines the filesystem architecture of Kitwork.

The filesystem itself is the runtime.

Every folder represents a runtime node.

Every runtime node begins with `router.kitwork.js`.

Requests never resolve the final path first.

Instead, they traverse the filesystem node-by-node.

---

example.com/
│
├── router.kitwork.js
│   # ============================================================
│   # ROOT ROUTER
│   #
│   # Entry point of the entire application.
│   #
│   # Every request enters here first.
│   #
│   # Available:
│   #
│   # router.guard(...)
│   # router.middleware(...)
│   # router.limit(...)
│   # router.cache(...)
│   # router.error(...)
│   #
│   # router.get()
│   # router.post()
│   # router.put()
│   # router.patch()
│   # router.delete()
│   #
│   # Router never declares its own path.
│   #
│   # The folder location IS the path.
│   #
│   # Request Flow
│   #
│   # Root Router
│   #      ↓
│   # Child Router
│   #      ↓
│   # Child Router
│   #      ↓
│   # Method
│   #
│   # ============================================================
│
├── index.kitwork.html
│   #
│   # View Composition
│   #
│   # Example
│   #
│   # <html>
│   #     {{ @head }}
│   # <body>
│   #     {{ @header }}
│   #     {{ @page }}
│   #     {{ @footer }}
│   # </body>
│   # </html>
│
├── page.kitwork.html
│   #
│   # Current page only.
│   #
│   # Never inherited.
│
├── notfound.kitwork.html
│   #
│   # Local NotFound View
│   #
│   # Bubble:
│   #
│   # Current
│   #   ↑
│   # Parent
│   #   ↑
│   # Root
│
├── head.kitwork.html
├── header.kitwork.html
├── footer.kitwork.html
├── sidebar.kitwork.html
├── navbar.kitwork.html
│
├── public/
│   ├── css/
│   ├── js/
│   ├── images/
│   └── fonts/
│
├── users/
│   │
│   ├── router.kitwork.js
│   │
│   │   # Folder Router
│   │   #
│   │   # Global rules for /users/*
│   │   #
│   │   # router.guard(...)
│   │   # router.error(...)
│   │   # router.limit(...)
│   │   #
│   │   # router.get()
│   │   # router.post()
│   │
│   ├── page.kitwork.html
│   ├── notfound.kitwork.html
│   │
│   ├── create/
│   │   │
│   │   ├── router.kitwork.js
│   │   ├── page.kitwork.html
│   │   └── notfound.kitwork.html
│   │
│   ├── search/
│   │   │
│   │   ├── router.kitwork.js
│   │   ├── page.kitwork.html
│   │   └── notfound.kitwork.html
│   │
│   └── {user}/
│       │
│       ├── router.kitwork.js
│       │
│       │   # Dynamic Folder
│       │   #
│       │   # Examples
│       │   #
│       │   # {user}
│       │   # {id[number]}
│       │   # {slug(regex)}
│       │   #
│       │   # preload user
│       │
│       ├── head.kitwork.html
│       ├── page.kitwork.html
│       ├── notfound.kitwork.html
│       │
│       ├── profile/
│       │   │
│       │   ├── router.kitwork.js
│       │   ├── page.kitwork.html
│       │   └── notfound.kitwork.html
│       │
│       ├── settings/
│       │   │
│       │   ├── router.kitwork.js
│       │   ├── page.kitwork.html
│       │   ├── notfound.kitwork.html
│       │   │
│       │   ├── security/
│       │   │   ├── router.kitwork.js
│       │   │   └── page.kitwork.html
│       │   │
│       │   └── password/
│       │       ├── router.kitwork.js
│       │       └── page.kitwork.html
│       │
│       ├── posts/
│       │   │
│       │   ├── router.kitwork.js
│       │   ├── page.kitwork.html
│       │   ├── notfound.kitwork.html
│       │   │
│       │   ├── create/
│       │   │   ├── router.kitwork.js
│       │   │   └── page.kitwork.html
│       │   │
│       │   └── {id[number]}/
│       │       │
│       │       ├── router.kitwork.js
│       │       │
│       │       │   # router.get()
│       │       │   # router.post()
│       │       │   # router.put()
│       │       │   # router.delete()
│       │       │
│       │       │   # Every Method can have
│       │       │   #
│       │       │   # guard()
│       │       │   # middleware()
│       │       │   # limit()
│       │       │   # cache()
│       │       │   # error()
│       │       │   # meta()
│       │       │   # handle()
│       │       │
│       │       ├── head.kitwork.html
│       │       ├── footer.kitwork.html
│       │       ├── page.kitwork.html
│       │       ├── notfound.kitwork.html
│       │       │
│       │       ├── comments/
│       │       │   │
│       │       │   ├── router.kitwork.js
│       │       │   ├── page.kitwork.html
│       │       │   ├── notfound.kitwork.html
│       │       │   │
│       │       │   ├── create/
│       │       │   │   ├── router.kitwork.js
│       │       │   │   └── page.kitwork.html
│       │       │   │
│       │       │   └── {comment[number]}/
│       │       │       ├── router.kitwork.js
│       │       │       ├── page.kitwork.html
│       │       │       └── notfound.kitwork.html
│       │       │
│       │       └── likes/
│       │           ├── router.kitwork.js
│       │           └── page.kitwork.html
│       │
│       ├── followers/
│       │   ├── router.kitwork.js
│       │   └── page.kitwork.html
│       │
│       └── following/
│           ├── router.kitwork.js
│           └── page.kitwork.html
│
├── blog/
│   │
│   ├── router.kitwork.js
│   ├── page.kitwork.html
│   ├── notfound.kitwork.html
│   ├── header.kitwork.html
│   │
│   ├── category/
│   │   ├── router.kitwork.js
│   │   ├── page.kitwork.html
│   │   └── {slug}/
│   │       ├── router.kitwork.js
│   │       └── page.kitwork.html
│   │
│   └── {slug}/
│       ├── router.kitwork.js
│       ├── page.kitwork.html
│       ├── notfound.kitwork.html
│       └── comments/
│           ├── router.kitwork.js
│           └── {id[number]}/
│               ├── router.kitwork.js
│               └── page.kitwork.html
│
├── dashboard/
│   │
│   ├── router.kitwork.js
│   │
│   ├── index.kitwork.html
│   │
│   ├── head.kitwork.html
│   ├── header.kitwork.html
│   ├── sidebar.kitwork.html
│   ├── footer.kitwork.html
│   ├── page.kitwork.html
│   ├── notfound.kitwork.html
│   │
│   ├── analytics/
│   │   ├── router.kitwork.js
│   │   └── page.kitwork.html
│   │
│   ├── users/
│   │   ├── router.kitwork.js
│   │   ├── page.kitwork.html
│   │   └── {id[number]}/
│   │       ├── router.kitwork.js
│   │       └── page.kitwork.html
│   │
│   └── settings/
│       ├── router.kitwork.js
│       ├── page.kitwork.html
│       └── permissions/
│           ├── router.kitwork.js
│           └── page.kitwork.html
│
└── api/
    │
    ├── router.kitwork.js
    │
    │   # API Runtime
    │   #
    │   # No View System
    │
    ├── auth/
    │   ├── router.kitwork.js
    │   └── login/
    │       └── router.kitwork.js
    │
    ├── users/
    │   ├── router.kitwork.js
    │   ├── search/
    │   │   └── router.kitwork.js
    │   │
    │   └── {id[number]}/
    │       ├── router.kitwork.js
    │       └── avatar/
    │           └── router.kitwork.js
    │
    └── posts/
        ├── router.kitwork.js
        └── {slug}/
            ├── router.kitwork.js
            └── comments/
                └── router.kitwork.js

---

# Router Resolution

Request

↓

Root Router

↓

Child Router

↓

Child Router

↓

Child Router

↓

Method

↓

Handler

Every request traverses every router.

---

# View Resolution

Current Folder

↓

page.kitwork.html

↓

Current Folder

↓

index.kitwork.html

↓

{{ @head }}

↓

{{ @header }}

↓

{{ @page }}

↓

{{ @footer }}

Slots resolve from

Current

↑

Parent

↑

Root

Nearest file wins.

---

# Page Resolution

page.kitwork.html

Current Folder Only

Never inherited.

---

# NotFound Resolution

Current Folder

↑

Parent

↑

Parent

↑

Root

Nearest notfound.kitwork.html wins.

---

# Error Resolution

Method Error

↓

Current Router Error

↓

Parent Router Error

↓

Root Router Error

Nearest error handler wins.

---

# Core Principles

• The filesystem is the routing tree.

• Every folder begins with a router.

• Router defines runtime behavior, not path.

• Folder location defines the URL.

• Runtime traverses Outside → Inside.

• Views resolve Inside → Outside.

• Page is local.

• Slots are inherited.

• NotFound bubbles upward.

• Errors bubble upward.

• Every HTTP Method is an independent runtime node inside a router.