# Publishing Outputs

Kitwork separates a document's meaning from its wire representation:

- `router.rss()` declares an RSS channel.
- `router.sitemap()` declares public URLs.
- `ctx.type(...).send(...)` sends arbitrary XML, CSV, calendar, or vendor media types.

RSS and sitemap declarations belong in the tenant's root `router.kitwork.js`. They create virtual
GET/HEAD endpoints; a real filesystem route at the same path takes precedence.

## RSS

```javascript
router.rss({
    path: "/rss.xml",
    title: "Huynh Nhan Quoc",
    description: "Systems, runtime design, and long learning.",
    link: "https://huynhnhanquoc.com",
    language: "en",
    items: () => posts().map((post) => ({
        title: post.title,
        description: post.summary,
        link: "/articles/" + post.slug,
        published: post.date,
        guid: post.id
    }))
}).cache("1h")
```

Required channel fields:

- `title`
- `description` (alias: `summary`)
- `link` (alias: `url`)
- `items` array (alias: `entries`)

Each item requires `link` and either `title` or `description`. Relative links are made absolute
against the request origin. Dates accept ISO 8601, `YYYY-MM-DD`, or RFC 1123 and are emitted as
RFC 1123Z.

The entire channel may also come from a provider:

```javascript
router.rss(() => ({
    title: "Engineering",
    description: "Field notes",
    link: "https://example.com",
    items: latestPosts()
}))
```

`path` is available on the object form and defaults to `/rss.xml`.

## Sitemap

```javascript
router.sitemap(() => collection.index().map((document) => ({
    loc: "/concepts/" + document.file.slug,
    lastmod: document.meta.updated,
    changefreq: "monthly",
    priority: "0.7"
}))).cache("6h")
```

A provider may return an array directly or an object containing `pages`, `entries`, or `items`.
Each entry may be a URL string or an object:

- `loc` (aliases: `path`, `url`)
- `lastmod` (aliases: `updated`, `date`)
- `changefreq`
- `priority`

Duplicate absolute URLs are removed. Above 50,000 URLs, `/sitemap.xml` becomes a sitemap index and
the same provider serves `/sitemap-1.xml`, `/sitemap-2.xml`, and so on. Each generated document is
also limited to 50 MB uncompressed.

## HTTP Contract

Semantic outputs participate in the same lifecycle as normal folder methods:

```javascript
router.rss(config)
    .guard(canPublish)
    .limit({ rate: 30, per: "1m" })
    .cache("1h")
    .persist("1d")
```

The engine emits:

- Correct media types (`application/rss+xml` or `application/xml`)
- `ETag` derived from the serialized document
- `Last-Modified` derived from the newest entry date
- Empty-body `HEAD` responses
- `304 Not Modified` for matching `If-None-Match` or `If-Modified-Since`

Validator headers are stored with RAM and disk response caches, so cache hits preserve the same
HTTP behavior without entering the VM.

## Arbitrary Representations

XML and CSV are response details rather than publishing concepts:

```javascript
router.get((ctx) => ctx
    .type("application/xml; charset=utf-8")
    .send(xmlDocument))

router.get((ctx) => ctx
    .type("text/csv; charset=utf-8")
    .send(csvDocument))
```

The media type must contain `/` and cannot contain line breaks. Typed responses support the normal
status and cache lifecycle.
