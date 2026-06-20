# Example — Phase 1 convention (paper prototype)

A concrete **before → after** for two real `kitwork.io` routes (`/users` and `/api/gold`),
refactored from the central `app.kitwork.js` into the folder-module convention from
[`../ARCHITECTURE.md`](../ARCHITECTURE.md).

> ⚠️ **Paper prototype.** These files won't *run* yet — the engine doesn't support folder
> mount / `ctx` injection / `kit.run()` capsules. The point is to **see and feel** the
> structure before committing engine work. Built on a copy, nothing live was touched.

Defaults used (rename trivially later): `app/`, `public/`, `index.kitwork.js`, `client.kitwork.js`.

---

## Before (today — everything in one file)

`app.kitwork.js` (~490 lines) holds the DB connection **and** every route inline:

```js
const db = database.connect("system");

api.get("/gold").cache("1h").handle((response) => {
    const fetch = http.get("https://edge-api.pnj.io/.../get-gold-price?zone=11");
    if (fetch.status != 200) { return response.status(500).json({ ... }); }
    const body = fetch.json();
    const data = body.data.map(item => ({ name: item.tensp, buy: item.giamua, sell: item.giaban }));
    return response.status(200).json({ success: true, count: data.length, data });
});

router.get("/users/:id?").handle((request, response) => {
    const page = home.page(request.page());
    const id = request.params("id");
    const binding = { request };
    if (!id) { binding.users = db.table("user").list(5); }
    else     { binding.user  = db.where(u => u.id == id).first(); }
    return response.html(page.bind(binding));
});

router.get("/*").handle(...)   // + ~40 more routes inline
```

Logic for `/users` is split across two places: the view in `views/users/…` and the
handler here. Hard to see one feature whole.

## After (this folder)

```
example/
  config.kitwork.yml
  app.kitwork.js                       ← thin import map (the whole site at a glance)
  app/
    users/
      index.kitwork.js                 ← list handler        (logic only)
      page.kitwork.html                ← list view           (template only)
      client.kitwork.js                ← capsule: live filter (tier ③, client only)
      [id]/
        index.kitwork.js               ← detail handler
        page.kitwork.html              ← detail view
    api/
      gold/index.kitwork.js            ← /api/gold            (no page = JSON API)
  lib/
    gold.kitwork.js                    ← reusable fetch logic (no routing)
```

Each feature is now **one folder**, with HTML and JS in **separate files**. `app.kitwork.js`
shrinks from ~490 lines of mixed logic to a ~12-line map.

## What to look at, in order

1. `app.kitwork.js` — the whole route map in one glance (explicit `router.use()`).
2. `app/users/index.kitwork.js` + `page.kitwork.html` — tier ① (SSR, trusted, SEO).
3. `app/users/client.kitwork.js` — tier ③ (the capsule: `kit.run(() => db.table("user")…)`).
4. `lib/gold.kitwork.js` + `app/api/gold/index.kitwork.js` — shared logic vs route.

Then judge: does this read **cleaner** than 490 mixed lines, or just cleaner on paper?
